package backuprpo

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const (
	testBody       = "hello world"
	testSHA256     = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
	testContentMD5 = "XrY7u+Ae7tCTyyK7j1rNww=="
	testBackupID   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testObjectKey  = "backup-rpo/g-9/a-5/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.bundle"
)

func TestNewYandexS3RejectsUnsafeConfiguration(t *testing.T) {
	t.Parallel()
	valid := validYandexS3Config()
	cases := []struct {
		name   string
		mutate func(*YandexS3Config)
	}{
		{name: "http endpoint", mutate: func(c *YandexS3Config) { c.Endpoint = "http://storage.example.invalid" }},
		{name: "endpoint credentials", mutate: func(c *YandexS3Config) { c.Endpoint = "https://user:pass@storage.example.invalid" }},
		{name: "endpoint path", mutate: func(c *YandexS3Config) { c.Endpoint = "https://storage.example.invalid/path" }},
		{name: "endpoint query", mutate: func(c *YandexS3Config) { c.Endpoint = "https://storage.example.invalid/?token=secret" }},
		{name: "empty region", mutate: func(c *YandexS3Config) { c.Region = "" }},
		{name: "spaced region", mutate: func(c *YandexS3Config) { c.Region = " ru-central1 " }},
		{name: "uppercase bucket", mutate: func(c *YandexS3Config) { c.Bucket = "Backup-Bucket" }},
		{name: "ip bucket", mutate: func(c *YandexS3Config) { c.Bucket = "192.0.2.1" }},
		{name: "empty access key", mutate: func(c *YandexS3Config) { c.AccessKeyID = "" }},
		{name: "empty secret key", mutate: func(c *YandexS3Config) { c.SecretAccessKey = "" }},
		{name: "spaced secret key", mutate: func(c *YandexS3Config) { c.SecretAccessKey = " synthetic-secret-key " }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid
			tc.mutate(&cfg)
			_, err := NewYandexS3(context.Background(), cfg, acceptManifestVerifier{})
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("error = %v, want ErrInvalidConfig", err)
			}
			if (cfg.AccessKeyID != "" && strings.Contains(err.Error(), cfg.AccessKeyID)) ||
				(cfg.SecretAccessKey != "" && strings.Contains(err.Error(), cfg.SecretAccessKey)) {
				t.Fatalf("configuration error leaked credentials: %q", err)
			}
		})
	}
	if _, err := NewYandexS3(context.Background(), valid, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil verifier error = %v, want ErrInvalidConfig", err)
	}
}

func TestNewYandexS3PinsStaticCredentialsRetryAndRedirectPolicy(t *testing.T) {
	t.Parallel()
	cfg := validYandexS3Config()
	store, err := NewYandexS3(context.Background(), cfg, acceptManifestVerifier{})
	if err != nil {
		t.Fatalf("NewYandexS3: %v", err)
	}
	client, ok := store.client.(*s3.Client)
	if !ok {
		t.Fatalf("client type = %T, want *s3.Client", store.client)
	}
	options := client.Options()
	if options.Region != "ru-central1" || valueOrZero(options.BaseEndpoint) != cfg.Endpoint {
		t.Fatalf("region/endpoint = %q/%q", options.Region, valueOrZero(options.BaseEndpoint))
	}
	if options.RetryMaxAttempts != 1 {
		t.Fatalf("RetryMaxAttempts = %d, want 1", options.RetryMaxAttempts)
	}
	if !options.UsePathStyle {
		t.Fatal("UsePathStyle = false, want true for the validated endpoint")
	}
	credentials, err := options.Credentials.Retrieve(context.Background())
	if err != nil {
		t.Fatalf("retrieve credentials: %v", err)
	}
	if credentials.AccessKeyID != cfg.AccessKeyID || credentials.SecretAccessKey != cfg.SecretAccessKey || credentials.SessionToken != "" {
		t.Fatal("constructor did not install the exact static credential pair")
	}
	httpClient, ok := options.HTTPClient.(*http.Client)
	if !ok || httpClient.CheckRedirect == nil {
		t.Fatalf("HTTP client = %T, want redirect-rejecting *http.Client", options.HTTPClient)
	}
	redirectErr := httpClient.CheckRedirect(&http.Request{}, []*http.Request{{}})
	if !errors.Is(redirectErr, http.ErrUseLastResponse) {
		t.Fatalf("redirect error = %v, want http.ErrUseLastResponse", redirectErr)
	}
}

func TestBuildObjectKeyBindsAttemptAndRejectsUnsafeIdentity(t *testing.T) {
	t.Parallel()
	key, err := BuildObjectKey(9, 5, testBackupID)
	if err != nil || key != testObjectKey {
		t.Fatalf("BuildObjectKey = %q, %v", key, err)
	}
	for _, tc := range []struct {
		generation int64
		sequence   int64
		backupID   string
	}{
		{0, 5, testBackupID},
		{9, 0, testBackupID},
		{9, 5, "../escape"},
		{9, 5, strings.Repeat("B", 32)},
	} {
		if _, err := BuildObjectKey(tc.generation, tc.sequence, tc.backupID); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("BuildObjectKey(%d,%d,%q) error = %v", tc.generation, tc.sequence, tc.backupID, err)
		}
	}
}

func TestCheckVersioningRequiresEnabledAndRedactsProviderErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		output *s3.GetBucketVersioningOutput
		err    error
		want   error
	}{
		{name: "enabled", output: &s3.GetBucketVersioningOutput{Status: types.BucketVersioningStatusEnabled}},
		{name: "absent", output: &s3.GetBucketVersioningOutput{}, want: ErrVersioningRequired},
		{name: "suspended", output: &s3.GetBucketVersioningOutput{Status: types.BucketVersioningStatusSuspended}, want: ErrVersioningRequired},
		{name: "provider error", err: errors.New("credential AKIA-SYNTHETIC rejected"), want: ErrStorageUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeS3{versioningFn: func(*s3.GetBucketVersioningInput) (*s3.GetBucketVersioningOutput, error) {
				return tc.output, tc.err
			}}
			store := mustTestStore(t, fake, acceptManifestVerifier{})
			err := store.CheckVersioning(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if err != nil && strings.Contains(err.Error(), "AKIA-SYNTHETIC") {
				t.Fatalf("error leaked provider detail: %q", err)
			}
			if got := fake.snapshot().versioningBuckets; len(got) != 1 || got[0] != "backup-bucket" {
				t.Fatalf("versioning buckets = %#v", got)
			}
		})
	}
}

func TestPutImmutablePrehashesRewindsAndSendsExactMetadata(t *testing.T) {
	t.Parallel()
	fake := &fakeS3{}
	store := mustTestStore(t, fake, acceptManifestVerifier{})
	body := bytes.NewReader([]byte(testBody))
	if _, err := body.Seek(6, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	version, err := store.PutImmutable(context.Background(), PutRequest{
		Key: testObjectKey, Body: body, Metadata: validMetadata(),
	})
	if err != nil || version.String() != "opaque-version-1" {
		t.Fatalf("PutImmutable = %q, %v", version.String(), err)
	}
	snapshot := fake.snapshot()
	if strings.Join(snapshot.events, ",") != "versioning,put" {
		t.Fatalf("events = %#v", snapshot.events)
	}
	if len(snapshot.puts) != 1 {
		t.Fatalf("put count = %d", len(snapshot.puts))
	}
	put := snapshot.puts[0]
	if put.bucket != "backup-bucket" || put.key != testObjectKey || put.contentLength != 11 || put.contentMD5 != testContentMD5 ||
		put.ifNoneMatch != "*" || string(put.body) != testBody {
		t.Fatalf("put = %#v", put)
	}
	wantMetadata := map[string]string{
		"maestro-sha256":      testSHA256,
		"size-bytes":          "11",
		"captured-generation": "9",
		"attempt-sequence":    "5",
		"backup-id":           testBackupID,
		"manifest-version":    "2",
		"lease-fence":         "3",
	}
	if !equalMetadata(put.metadata, wantMetadata) {
		t.Fatalf("metadata = %#v, want %#v", put.metadata, wantMetadata)
	}
}

func TestPutImmutableRejectsBoundsDigestAndUnsafeKeyBeforeS3(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		key    string
		body   string
		mutate func(*ObjectMetadata)
	}{
		{name: "wrong key", key: "backup-rpo/shared.bundle", body: testBody},
		{name: "wrong size", key: testObjectKey, body: testBody, mutate: func(m *ObjectMetadata) { m.SizeBytes = 10 }},
		{name: "wrong digest", key: testObjectKey, body: testBody, mutate: func(m *ObjectMetadata) { m.SHA256 = strings.Repeat("a", 64) }},
		{name: "empty", key: testObjectKey, body: "", mutate: func(m *ObjectMetadata) { m.SizeBytes = 0; m.SHA256 = strings.Repeat("0", 64) }},
		{name: "over one gib", key: testObjectKey, body: "", mutate: func(m *ObjectMetadata) { m.SizeBytes = MaxObjectBytes + 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			metadata := validMetadata()
			if tc.mutate != nil {
				tc.mutate(&metadata)
			}
			fake := &fakeS3{}
			store := mustTestStore(t, fake, acceptManifestVerifier{})
			_, err := store.PutImmutable(context.Background(), PutRequest{Key: tc.key, Body: bytes.NewReader([]byte(tc.body)), Metadata: metadata})
			if !errors.Is(err, ErrInvalidRequest) && !errors.Is(err, ErrObjectMismatch) {
				t.Fatalf("error = %v", err)
			}
			snapshot := fake.snapshot()
			if len(snapshot.events) != 0 {
				t.Fatalf("S3 called for invalid request: %#v", snapshot.events)
			}
		})
	}
}

func TestPutImmutableTreatsCallFailureAsUnknownAndRequiresOpaqueVersion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		putOutput *s3.PutObjectOutput
		putErr    error
		want      error
	}{
		{name: "unknown transport", putErr: errors.New("secret=provider-detail"), want: ErrPutOutcomeUnknown},
		{name: "missing version", putOutput: &s3.PutObjectOutput{}, want: ErrVersioningRequired},
		{name: "null version", putOutput: &s3.PutObjectOutput{VersionId: ptrTo("null")}, want: ErrVersioningRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeS3{putFn: func(*s3.PutObjectInput) (*s3.PutObjectOutput, error) { return tc.putOutput, tc.putErr }}
			store := mustTestStore(t, fake, acceptManifestVerifier{})
			_, err := store.PutImmutable(context.Background(), validPutRequest())
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if strings.Contains(err.Error(), "provider-detail") {
				t.Fatalf("error leaked provider detail: %q", err)
			}
		})
	}
}

func TestGetExactStreamsBoundedBodyAndVerifiesManifest(t *testing.T) {
	t.Parallel()
	body := newTrackedBody(testBody, nil)
	verifier := &recordingVerifier{}
	fake := &fakeS3{getFn: func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
		return validGetOutput(body, valueOrZero(input.VersionId)), nil
	}}
	store := mustTestStore(t, fake, verifier)
	version := mustVersionID(t, "opaque-version-1")
	readback, err := store.GetExact(context.Background(), ExactObjectRequest{
		Key: testObjectKey, VersionID: version, Metadata: validMetadata(),
	})
	if err != nil {
		t.Fatalf("GetExact: %v", err)
	}
	if readback.VersionID != version || readback.SHA256 != testSHA256 || readback.SizeBytes != 11 || !readback.ManifestAuthenticated {
		t.Fatalf("readback = %#v", readback)
	}
	if !body.isClosed() {
		t.Fatal("response body was not closed")
	}
	vSnapshot := verifier.snapshot()
	if vSnapshot.calls != 1 || string(vSnapshot.body) != testBody || vSnapshot.expectation != validManifestExpectation(version) {
		t.Fatalf("verifier = %#v", vSnapshot)
	}
	snapshot := fake.snapshot()
	if strings.Join(snapshot.events, ",") != "versioning,get" {
		t.Fatalf("events = %#v", snapshot.events)
	}
	if len(snapshot.gets) != 1 || snapshot.gets[0].key != testObjectKey || snapshot.gets[0].version != version.String() {
		t.Fatalf("gets = %#v", snapshot.gets)
	}
}

func TestGetExactFailsClosedOnReadbackBoundaries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		output func(*trackedBody) *s3.GetObjectOutput
		getErr error
		verify error
		close  error
		want   error
	}{
		{name: "provider timeout", getErr: context.DeadlineExceeded, want: ErrStorageUnavailable},
		{name: "delete marker", output: func(b *trackedBody) *s3.GetObjectOutput {
			o := validGetOutput(b, "opaque-version-1")
			o.DeleteMarker = ptrTo(true)
			return o
		}, want: ErrObjectMismatch},
		{name: "wrong version", output: func(b *trackedBody) *s3.GetObjectOutput { return validGetOutput(b, "other-version") }, want: ErrObjectMismatch},
		{name: "missing metadata", output: func(b *trackedBody) *s3.GetObjectOutput {
			o := validGetOutput(b, "opaque-version-1")
			delete(o.Metadata, "lease-fence")
			return o
		}, want: ErrObjectMismatch},
		{name: "extra metadata", output: func(b *trackedBody) *s3.GetObjectOutput {
			o := validGetOutput(b, "opaque-version-1")
			o.Metadata["foreign"] = "x"
			return o
		}, want: ErrObjectMismatch},
		{name: "declared length", output: func(b *trackedBody) *s3.GetObjectOutput {
			o := validGetOutput(b, "opaque-version-1")
			o.ContentLength = int64Ptr(12)
			return o
		}, want: ErrObjectMismatch},
		{name: "stream overrun", output: func(b *trackedBody) *s3.GetObjectOutput { return validGetOutput(b, "opaque-version-1") }, want: ErrObjectMismatch},
		{name: "manifest", output: func(b *trackedBody) *s3.GetObjectOutput { return validGetOutput(b, "opaque-version-1") }, verify: errors.New("signature detail"), want: ErrManifestInvalid},
		{name: "close", output: func(b *trackedBody) *s3.GetObjectOutput { return validGetOutput(b, "opaque-version-1") }, close: errors.New("close detail"), want: ErrObjectMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			contents := testBody
			if tc.name == "stream overrun" {
				contents = testBody + "!"
			}
			body := newTrackedBody(contents, tc.close)
			verifier := &recordingVerifier{err: tc.verify}
			fake := &fakeS3{getFn: func(*s3.GetObjectInput) (*s3.GetObjectOutput, error) {
				if tc.getErr != nil {
					return nil, tc.getErr
				}
				return tc.output(body), nil
			}}
			store := mustTestStore(t, fake, verifier)
			_, err := store.GetExact(context.Background(), validExactRequest(t, "opaque-version-1"))
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if err != nil && (strings.Contains(err.Error(), "signature detail") || strings.Contains(err.Error(), "close detail")) {
				t.Fatalf("error leaked dependency detail: %q", err)
			}
			if tc.getErr == nil && !body.isClosed() {
				t.Fatal("body not closed on failure")
			}
		})
	}
}

func TestGetExactClosesResponseBodyWhenProviderReturnsOutputAndError(t *testing.T) {
	t.Parallel()
	body := newTrackedBody(testBody, nil)
	fake := &fakeS3{getFn: func(*s3.GetObjectInput) (*s3.GetObjectOutput, error) {
		return validGetOutput(body, "opaque-version-1"), errors.New("credential-bearing provider detail")
	}}
	store := mustTestStore(t, fake, acceptManifestVerifier{})

	_, err := store.GetExact(context.Background(), validExactRequest(t, "opaque-version-1"))
	if !errors.Is(err, ErrStorageUnavailable) {
		t.Fatalf("error = %v, want ErrStorageUnavailable", err)
	}
	if strings.Contains(err.Error(), "credential-bearing") {
		t.Fatalf("error leaked provider detail: %q", err)
	}
	if !body.isClosed() {
		t.Fatal("response body returned alongside provider error was not closed")
	}
}

func TestReconcileUnknownPutUsesBothMarkersAndAdoptsOneExactVersion(t *testing.T) {
	t.Parallel()
	fake := &fakeS3{listOutputs: []*s3.ListObjectVersionsOutput{
		{
			IsTruncated:         ptrTo(true),
			NextKeyMarker:       ptrTo(testObjectKey),
			NextVersionIdMarker: ptrTo("marker-v1"),
			Versions:            []types.ObjectVersion{{Key: ptrTo(testObjectKey), VersionId: ptrTo("mismatch-size"), Size: int64Ptr(10), IsLatest: ptrTo(false)}},
		},
		{
			IsTruncated: ptrTo(false),
			Versions:    []types.ObjectVersion{{Key: ptrTo(testObjectKey), VersionId: ptrTo("opaque-version-1"), Size: int64Ptr(11), IsLatest: ptrTo(false)}},
		},
	}, getFn: func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
		return validGetOutput(newTrackedBody(testBody, nil), valueOrZero(input.VersionId)), nil
	}}
	store := mustTestStore(t, fake, acceptManifestVerifier{})
	version, err := store.ReconcileUnknownPut(context.Background(), ReconcileRequest{Key: testObjectKey, Metadata: validMetadata()})
	if err != nil || version.String() != "opaque-version-1" {
		t.Fatalf("ReconcileUnknownPut = %q, %v", version.String(), err)
	}
	snapshot := fake.snapshot()
	if len(snapshot.lists) != 2 {
		t.Fatalf("list count = %d", len(snapshot.lists))
	}
	if snapshot.lists[0].keyMarker != "" || snapshot.lists[0].versionMarker != "" || snapshot.lists[0].maxKeys != 100 {
		t.Fatalf("first page input = %#v", snapshot.lists[0])
	}
	if snapshot.lists[1].keyMarker != testObjectKey || snapshot.lists[1].versionMarker != "marker-v1" || snapshot.lists[1].maxKeys != 100 {
		t.Fatalf("second page input = %#v", snapshot.lists[1])
	}
}

func TestReconcileUnknownPutIgnoresIsLatestForExactVersionEvidence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		isLatest *bool
	}{
		{name: "true", isLatest: ptrTo(true)},
		{name: "false", isLatest: ptrTo(false)},
		{name: "absent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeS3{
				listOutputs: []*s3.ListObjectVersionsOutput{{
					IsTruncated: ptrTo(false),
					Versions: []types.ObjectVersion{{
						Key: ptrTo(testObjectKey), VersionId: ptrTo("opaque-version-1"),
						Size: int64Ptr(11), IsLatest: tc.isLatest,
					}},
				}},
				getFn: func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
					return validGetOutput(newTrackedBody(testBody, nil), valueOrZero(input.VersionId)), nil
				},
			}
			store := mustTestStore(t, fake, acceptManifestVerifier{})
			version, err := store.ReconcileUnknownPut(context.Background(), ReconcileRequest{Key: testObjectKey, Metadata: validMetadata()})
			if err != nil || version.String() != "opaque-version-1" {
				t.Fatalf("ReconcileUnknownPut = %q, %v", version.String(), err)
			}
			snapshot := fake.snapshot()
			if len(snapshot.gets) != 1 || snapshot.gets[0].version != "opaque-version-1" {
				t.Fatalf("exact gets = %#v", snapshot.gets)
			}
		})
	}
}

func TestReconcileUnknownPutLeavesZeroMismatchAndAmbiguityUnresolved(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		versions []types.ObjectVersion
		getFn    func(*s3.GetObjectInput) (*s3.GetObjectOutput, error)
		want     error
	}{
		{name: "zero", want: ErrReconcileUnresolved},
		{name: "digest mismatch", versions: []types.ObjectVersion{{Key: ptrTo(testObjectKey), VersionId: ptrTo("v1"), Size: int64Ptr(11), IsLatest: ptrTo(false)}}, getFn: func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
			o := validGetOutput(newTrackedBody("hello worle", nil), valueOrZero(input.VersionId))
			return o, nil
		}, want: ErrReconcileUnresolved},
		{name: "multiple exact", versions: []types.ObjectVersion{
			{Key: ptrTo(testObjectKey), VersionId: ptrTo("v1"), Size: int64Ptr(11), IsLatest: ptrTo(false)},
			{Key: ptrTo(testObjectKey), VersionId: ptrTo("v2"), Size: int64Ptr(11), IsLatest: ptrTo(false)},
		}, getFn: func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
			return validGetOutput(newTrackedBody(testBody, nil), valueOrZero(input.VersionId)), nil
		}, want: ErrReconcileAmbiguous},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeS3{listOutputs: []*s3.ListObjectVersionsOutput{{IsTruncated: ptrTo(false), Versions: tc.versions}}, getFn: tc.getFn}
			store := mustTestStore(t, fake, acceptManifestVerifier{})
			_, err := store.ReconcileUnknownPut(context.Background(), ReconcileRequest{Key: testObjectKey, Metadata: validMetadata()})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReconcileUnknownPutRejectsDeleteForeignAndBrokenPagination(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		output *s3.ListObjectVersionsOutput
		want   error
	}{
		{name: "delete marker", output: &s3.ListObjectVersionsOutput{DeleteMarkers: []types.DeleteMarkerEntry{{Key: ptrTo(testObjectKey), VersionId: ptrTo("deleted")}}}, want: ErrReconcileAmbiguous},
		{name: "foreign key", output: &s3.ListObjectVersionsOutput{Versions: []types.ObjectVersion{{Key: ptrTo(testObjectKey + "-foreign"), VersionId: ptrTo("v1"), Size: int64Ptr(11), IsLatest: ptrTo(false)}}}, want: ErrReconcileAmbiguous},
		{name: "missing truncation flag", output: &s3.ListObjectVersionsOutput{}, want: ErrPaginationInvalid},
		{name: "missing marker", output: &s3.ListObjectVersionsOutput{IsTruncated: ptrTo(true), NextKeyMarker: ptrTo(testObjectKey)}, want: ErrPaginationInvalid},
		{name: "too many entries", output: listWithVersions(101, false), want: ErrPaginationInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeS3{listOutputs: []*s3.ListObjectVersionsOutput{tc.output}}
			store := mustTestStore(t, fake, acceptManifestVerifier{})
			_, err := store.ReconcileUnknownPut(context.Background(), ReconcileRequest{Key: testObjectKey, Metadata: validMetadata()})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReconcileUnknownPutRejectsPaginationCyclesAndDuplicateVersions(t *testing.T) {
	t.Parallel()
	markerPage := func(keyMarker, versionMarker string) *s3.ListObjectVersionsOutput {
		return &s3.ListObjectVersionsOutput{
			IsTruncated: ptrTo(true), NextKeyMarker: ptrTo(keyMarker),
			NextVersionIdMarker: ptrTo(versionMarker),
		}
	}
	duplicateVersion := types.ObjectVersion{
		Key: ptrTo(testObjectKey), VersionId: ptrTo("duplicate-v1"), Size: int64Ptr(10),
	}
	cases := []struct {
		name  string
		pages []*s3.ListObjectVersionsOutput
		want  error
		calls int
	}{
		{
			name:  "repeated marker pair",
			pages: []*s3.ListObjectVersionsOutput{markerPage("key-a", "version-1"), markerPage("key-a", "version-1")},
			want:  ErrPaginationInvalid, calls: 2,
		},
		{
			name: "multi-step marker pair cycle",
			pages: []*s3.ListObjectVersionsOutput{
				markerPage("key-a", "version-1"), markerPage("key-b", "version-2"), markerPage("key-a", "version-1"),
			},
			want: ErrPaginationInvalid, calls: 3,
		},
		{
			name: "stalled key marker with cycling version marker",
			pages: []*s3.ListObjectVersionsOutput{
				markerPage("key-a", "version-1"), markerPage("key-a", "version-2"), markerPage("key-a", "version-1"),
			},
			want: ErrPaginationInvalid, calls: 3,
		},
		{
			name: "stalled version marker with cycling key marker",
			pages: []*s3.ListObjectVersionsOutput{
				markerPage("key-a", "version-1"), markerPage("key-b", "version-1"), markerPage("key-a", "version-1"),
			},
			want: ErrPaginationInvalid, calls: 3,
		},
		{
			name: "duplicate version across pages",
			pages: []*s3.ListObjectVersionsOutput{
				{IsTruncated: ptrTo(true), NextKeyMarker: ptrTo("key-a"), NextVersionIdMarker: ptrTo("version-1"), Versions: []types.ObjectVersion{duplicateVersion}},
				{IsTruncated: ptrTo(false), Versions: []types.ObjectVersion{duplicateVersion}},
			},
			want: ErrReconcileAmbiguous, calls: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeS3{listOutputs: tc.pages}
			store := mustTestStore(t, fake, acceptManifestVerifier{})
			version, err := store.ReconcileUnknownPut(context.Background(), ReconcileRequest{Key: testObjectKey, Metadata: validMetadata()})
			if !errors.Is(err, tc.want) || version.String() != "" {
				t.Fatalf("ReconcileUnknownPut = %q, %v, want %v", version.String(), err, tc.want)
			}
			if got := len(fake.snapshot().lists); got != tc.calls || got > maxListPages {
				t.Fatalf("list calls = %d, want %d and bounded", got, tc.calls)
			}
		})
	}
}

func TestReconcileUnknownPutStopsAfterTenPagesAndRedactsTimeout(t *testing.T) {
	t.Parallel()
	pages := make([]*s3.ListObjectVersionsOutput, 10)
	for i := range pages {
		pages[i] = &s3.ListObjectVersionsOutput{
			IsTruncated:         ptrTo(true),
			NextKeyMarker:       ptrTo(testObjectKey),
			NextVersionIdMarker: ptrTo("marker-" + string(rune('a'+i))),
		}
	}
	fake := &fakeS3{listOutputs: pages}
	store := mustTestStore(t, fake, acceptManifestVerifier{})
	_, err := store.ReconcileUnknownPut(context.Background(), ReconcileRequest{Key: testObjectKey, Metadata: validMetadata()})
	if !errors.Is(err, ErrPaginationLimit) {
		t.Fatalf("error = %v, want ErrPaginationLimit", err)
	}
	if got := len(fake.snapshot().lists); got != 10 {
		t.Fatalf("list calls = %d, want 10", got)
	}

	timeoutFake := &fakeS3{listErr: errors.New("credential-bearing timeout detail")}
	timeoutStore := mustTestStore(t, timeoutFake, acceptManifestVerifier{})
	_, err = timeoutStore.ReconcileUnknownPut(context.Background(), ReconcileRequest{Key: testObjectKey, Metadata: validMetadata()})
	if !errors.Is(err, ErrStorageUnavailable) || strings.Contains(err.Error(), "credential-bearing") {
		t.Fatalf("timeout error = %q", err)
	}
}

func TestFakeS3AndAdapterAreRaceSafe(t *testing.T) {
	fake := &fakeS3{getFn: func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
		return validGetOutput(newTrackedBody(testBody, nil), valueOrZero(input.VersionId)), nil
	}}
	store := mustTestStore(t, fake, acceptManifestVerifier{})
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.CheckVersioning(context.Background()); err != nil {
				t.Errorf("CheckVersioning: %v", err)
			}
			if _, err := store.PutImmutable(context.Background(), validPutRequest()); err != nil {
				t.Errorf("PutImmutable: %v", err)
			}
			if _, err := store.GetExact(context.Background(), validExactRequest(t, "opaque-version-1")); err != nil {
				t.Errorf("GetExact: %v", err)
			}
		}()
	}
	wg.Wait()
	snapshot := fake.snapshot()
	if len(snapshot.puts) != 12 || len(snapshot.gets) != 12 {
		t.Fatalf("put/get calls = %d/%d", len(snapshot.puts), len(snapshot.gets))
	}
}

func validYandexS3Config() YandexS3Config {
	return YandexS3Config{
		Endpoint:        "https://storage.example.invalid",
		Region:          "ru-central1",
		Bucket:          "backup-bucket",
		AccessKeyID:     "SYNTHETIC-ACCESS-KEY",
		SecretAccessKey: "synthetic-secret-key",
	}
}

func validMetadata() ObjectMetadata {
	return ObjectMetadata{
		SHA256:             testSHA256,
		SizeBytes:          11,
		CapturedGeneration: 9,
		AttemptSequence:    5,
		BackupID:           testBackupID,
		ManifestVersion:    2,
		LeaseFence:         3,
	}
}

func validPutRequest() PutRequest {
	return PutRequest{Key: testObjectKey, Body: bytes.NewReader([]byte(testBody)), Metadata: validMetadata()}
}

func validExactRequest(t *testing.T, version string) ExactObjectRequest {
	t.Helper()
	return ExactObjectRequest{Key: testObjectKey, VersionID: mustVersionID(t, version), Metadata: validMetadata()}
}

func validManifestExpectation(version VersionID) ManifestExpectation {
	return ManifestExpectation{Key: testObjectKey, VersionID: version, Metadata: validMetadata()}
}

func mustVersionID(t *testing.T, raw string) VersionID {
	t.Helper()
	version, err := NewVersionID(raw)
	if err != nil {
		t.Fatalf("NewVersionID(%q): %v", raw, err)
	}
	return version
}

func mustTestStore(t *testing.T, fake *fakeS3, verifier AuthenticatedManifestVerifier) *YandexS3 {
	t.Helper()
	store, err := newYandexS3WithClient(validYandexS3Config(), fake, verifier)
	if err != nil {
		t.Fatalf("newYandexS3WithClient: %v", err)
	}
	return store
}

func validGetOutput(body io.ReadCloser, version string) *s3.GetObjectOutput {
	return &s3.GetObjectOutput{
		Body:          body,
		ContentLength: int64Ptr(11),
		DeleteMarker:  ptrTo(false),
		Metadata: map[string]string{
			"maestro-sha256":      testSHA256,
			"size-bytes":          "11",
			"captured-generation": "9",
			"attempt-sequence":    "5",
			"backup-id":           testBackupID,
			"manifest-version":    "2",
			"lease-fence":         "3",
		},
		VersionId: ptrTo(version),
	}
}

func listWithVersions(count int, truncated bool) *s3.ListObjectVersionsOutput {
	versions := make([]types.ObjectVersion, count)
	for i := range versions {
		versions[i] = types.ObjectVersion{Key: ptrTo(testObjectKey), VersionId: ptrTo("v"), Size: int64Ptr(10), IsLatest: ptrTo(false)}
	}
	return &s3.ListObjectVersionsOutput{IsTruncated: ptrTo(truncated), Versions: versions}
}

type acceptManifestVerifier struct{}

func (acceptManifestVerifier) VerifyAuthenticatedManifest(context.Context, io.Reader, ManifestExpectation) error {
	return nil
}

type verifierSnapshot struct {
	calls       int
	body        []byte
	expectation ManifestExpectation
}

type recordingVerifier struct {
	mu          sync.Mutex
	calls       int
	body        []byte
	expectation ManifestExpectation
	err         error
}

func (v *recordingVerifier) VerifyAuthenticatedManifest(_ context.Context, reader io.Reader, expectation ManifestExpectation) error {
	body, readErr := io.ReadAll(reader)
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls++
	v.body = append([]byte(nil), body...)
	v.expectation = expectation
	if readErr != nil {
		return readErr
	}
	return v.err
}

func (v *recordingVerifier) snapshot() verifierSnapshot {
	v.mu.Lock()
	defer v.mu.Unlock()
	return verifierSnapshot{calls: v.calls, body: append([]byte(nil), v.body...), expectation: v.expectation}
}

type trackedBody struct {
	mu       sync.Mutex
	reader   *bytes.Reader
	closed   bool
	closeErr error
}

func newTrackedBody(contents string, closeErr error) *trackedBody {
	return &trackedBody{reader: bytes.NewReader([]byte(contents)), closeErr: closeErr}
}

func (b *trackedBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reader.Read(p)
}

func (b *trackedBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	return b.closeErr
}

func (b *trackedBody) isClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

type putRecord struct {
	bucket        string
	key           string
	contentLength int64
	contentMD5    string
	ifNoneMatch   string
	metadata      map[string]string
	body          []byte
}

type getRecord struct {
	bucket  string
	key     string
	version string
}

type listRecord struct {
	bucket        string
	prefix        string
	keyMarker     string
	versionMarker string
	maxKeys       int32
}

type fakeSnapshot struct {
	events            []string
	versioningBuckets []string
	puts              []putRecord
	gets              []getRecord
	lists             []listRecord
}

type fakeS3 struct {
	mu                sync.Mutex
	events            []string
	versioningBuckets []string
	puts              []putRecord
	gets              []getRecord
	lists             []listRecord
	versioningFn      func(*s3.GetBucketVersioningInput) (*s3.GetBucketVersioningOutput, error)
	putFn             func(*s3.PutObjectInput) (*s3.PutObjectOutput, error)
	getFn             func(*s3.GetObjectInput) (*s3.GetObjectOutput, error)
	listOutputs       []*s3.ListObjectVersionsOutput
	listErr           error
	listIndex         int
}

func (f *fakeS3) GetBucketVersioning(_ context.Context, input *s3.GetBucketVersioningInput, _ ...func(*s3.Options)) (*s3.GetBucketVersioningOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "versioning")
	f.versioningBuckets = append(f.versioningBuckets, valueOrZero(input.Bucket))
	if f.versioningFn != nil {
		return f.versioningFn(input)
	}
	return &s3.GetBucketVersioningOutput{Status: types.BucketVersioningStatusEnabled}, nil
}

func (f *fakeS3) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "put")
	body, err := io.ReadAll(input.Body)
	if err != nil {
		return nil, err
	}
	f.puts = append(f.puts, putRecord{
		bucket: valueOrZero(input.Bucket), key: valueOrZero(input.Key),
		contentLength: valueOrZero(input.ContentLength), contentMD5: valueOrZero(input.ContentMD5),
		ifNoneMatch: valueOrZero(input.IfNoneMatch),
		metadata:    cloneMetadata(input.Metadata), body: append([]byte(nil), body...),
	})
	if f.putFn != nil {
		return f.putFn(input)
	}
	return &s3.PutObjectOutput{VersionId: ptrTo("opaque-version-1")}, nil
}

func (f *fakeS3) GetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "get")
	f.gets = append(f.gets, getRecord{bucket: valueOrZero(input.Bucket), key: valueOrZero(input.Key), version: valueOrZero(input.VersionId)})
	if f.getFn == nil {
		return nil, errors.New("fake S3 GetObject has no response")
	}
	return f.getFn(input)
}

func (f *fakeS3) ListObjectVersions(_ context.Context, input *s3.ListObjectVersionsInput, _ ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "list")
	f.lists = append(f.lists, listRecord{
		bucket: valueOrZero(input.Bucket), prefix: valueOrZero(input.Prefix),
		keyMarker: valueOrZero(input.KeyMarker), versionMarker: valueOrZero(input.VersionIdMarker),
		maxKeys: valueOrZero(input.MaxKeys),
	})
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listIndex >= len(f.listOutputs) {
		return &s3.ListObjectVersionsOutput{IsTruncated: ptrTo(false)}, nil
	}
	output := f.listOutputs[f.listIndex]
	f.listIndex++
	return output, nil
}

func (f *fakeS3) snapshot() fakeSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeSnapshot{
		events:            append([]string(nil), f.events...),
		versioningBuckets: append([]string(nil), f.versioningBuckets...),
		puts:              append([]putRecord(nil), f.puts...),
		gets:              append([]getRecord(nil), f.gets...),
		lists:             append([]listRecord(nil), f.lists...),
	}
}

func cloneMetadata(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func equalMetadata(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}
