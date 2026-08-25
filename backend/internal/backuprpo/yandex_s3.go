package backuprpo

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const (
	metadataSHA256                   = "maestro-sha256"
	metadataSizeBytes                = "size-bytes"
	metadataCapturedGeneration       = "captured-generation"
	metadataAttemptSequence          = "attempt-sequence"
	metadataBackupID                 = "backup-id"
	metadataManifestVersion          = "manifest-version"
	metadataLeaseFence               = "lease-fence"
	metadataFieldCount               = 7
	maxListKeys                int32 = 100
	maxListPages                     = 10
	maxListedEntries                 = 1000
)

type YandexS3Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
}

type s3API interface {
	GetBucketVersioning(context.Context, *s3.GetBucketVersioningInput, ...func(*s3.Options)) (*s3.GetBucketVersioningOutput, error)
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	ListObjectVersions(context.Context, *s3.ListObjectVersionsInput, ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error)
}

type YandexS3 struct {
	bucket   string
	client   s3API
	verifier AuthenticatedManifestVerifier
}

var _ ObjectStore = (*YandexS3)(nil)

func NewYandexS3(ctx context.Context, config YandexS3Config, verifier AuthenticatedManifestVerifier) (*YandexS3, error) {
	if !isValidYandexS3Config(config) || verifier == nil {
		return nil, ErrInvalidConfig
	}
	httpClient := &http.Client{
		Transport: http.DefaultTransport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	loaded, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion(config.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			config.AccessKeyID, config.SecretAccessKey, "",
		)),
		awsconfig.WithHTTPClient(httpClient),
		awsconfig.WithRetryMaxAttempts(1),
	)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	client := s3.NewFromConfig(loaded, func(options *s3.Options) {
		options.BaseEndpoint = ptrTo(config.Endpoint)
		options.UsePathStyle = true
		options.RetryMaxAttempts = 1
		options.HTTPClient = httpClient
	})
	return newYandexS3WithClient(config, client, verifier)
}

func newYandexS3WithClient(config YandexS3Config, client s3API, verifier AuthenticatedManifestVerifier) (*YandexS3, error) {
	if !isValidYandexS3Config(config) || client == nil || verifier == nil {
		return nil, ErrInvalidConfig
	}
	return &YandexS3{bucket: config.Bucket, client: client, verifier: verifier}, nil
}

func (store *YandexS3) CheckVersioning(ctx context.Context) error {
	if store == nil || store.client == nil {
		return ErrStorageUnavailable
	}
	output, err := store.client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{
		Bucket: ptrTo(store.bucket),
	})
	if err != nil || output == nil {
		return ErrStorageUnavailable
	}
	if output.Status != types.BucketVersioningStatusEnabled {
		return ErrVersioningRequired
	}
	return nil
}

func (store *YandexS3) PutImmutable(ctx context.Context, request PutRequest) (VersionID, error) {
	if store == nil || store.client == nil || request.Body == nil ||
		!isValidObjectMetadata(request.Metadata) || !validBoundKey(request.Key, request.Metadata) {
		return VersionID{}, ErrInvalidRequest
	}
	contentMD5, err := prehashAndRewind(request)
	if err != nil {
		return VersionID{}, err
	}
	if err := store.CheckVersioning(ctx); err != nil {
		return VersionID{}, err
	}
	output, putErr := store.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        ptrTo(store.bucket),
		Key:           ptrTo(request.Key),
		Body:          request.Body,
		ContentLength: ptrTo(request.Metadata.SizeBytes),
		ContentMD5:    ptrTo(contentMD5),
		IfNoneMatch:   ptrTo("*"),
		Metadata:      objectMetadata(request.Metadata),
	})
	if putErr != nil {
		return VersionID{}, ErrPutOutcomeUnknown
	}
	if output == nil || output.VersionId == nil {
		return VersionID{}, ErrVersioningRequired
	}
	version, versionErr := NewVersionID(*output.VersionId)
	if versionErr != nil {
		return VersionID{}, ErrVersioningRequired
	}
	return version, nil
}

func (store *YandexS3) GetExact(ctx context.Context, request ExactObjectRequest) (Readback, error) {
	if !isValidExactObjectRequest(request) {
		return Readback{}, ErrInvalidRequest
	}
	if err := store.CheckVersioning(ctx); err != nil {
		return Readback{}, err
	}
	return store.getExact(ctx, request)
}

func (store *YandexS3) getExact(ctx context.Context, request ExactObjectRequest) (readback Readback, resultErr error) {
	output, err := store.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket:    ptrTo(store.bucket),
		Key:       ptrTo(request.Key),
		VersionId: ptrTo(request.VersionID.String()),
	})
	if err != nil {
		if output != nil && output.Body != nil {
			_ = output.Body.Close()
		}
		return Readback{}, ErrStorageUnavailable
	}
	if output == nil {
		return Readback{}, ErrStorageUnavailable
	}
	if output.Body == nil {
		return Readback{}, ErrObjectMismatch
	}
	defer func() {
		if closeErr := output.Body.Close(); closeErr != nil {
			readback = Readback{}
			resultErr = ErrObjectMismatch
		}
	}()
	if valueOrZero(output.DeleteMarker) || output.ContentLength == nil ||
		*output.ContentLength != request.Metadata.SizeBytes || output.VersionId == nil ||
		*output.VersionId != request.VersionID.String() || valueOrZero(output.MissingMeta) != 0 ||
		!metadataMatches(output.Metadata, request.Metadata) {
		return Readback{}, ErrObjectMismatch
	}
	hasher := sha256.New()
	counter := &byteCounter{}
	bounded := io.LimitReader(output.Body, MaxObjectBytes+1)
	verifiedStream := io.TeeReader(bounded, io.MultiWriter(hasher, counter))
	expectation := ManifestExpectation{Key: request.Key, VersionID: request.VersionID, Metadata: request.Metadata}
	if err := store.verifier.VerifyAuthenticatedManifest(ctx, verifiedStream, expectation); err != nil {
		return Readback{}, ErrManifestInvalid
	}
	if _, err := io.Copy(io.Discard, verifiedStream); err != nil {
		return Readback{}, ErrObjectMismatch
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if counter.count != request.Metadata.SizeBytes || counter.count > MaxObjectBytes || digest != request.Metadata.SHA256 {
		return Readback{}, ErrObjectMismatch
	}
	return Readback{
		VersionID: request.VersionID, SHA256: digest, SizeBytes: counter.count,
		ManifestAuthenticated: true,
	}, nil
}

func (store *YandexS3) ReconcileUnknownPut(ctx context.Context, request ReconcileRequest) (VersionID, error) {
	if store == nil || store.client == nil || !isValidObjectMetadata(request.Metadata) ||
		!validBoundKey(request.Key, request.Metadata) {
		return VersionID{}, ErrInvalidRequest
	}
	if err := store.CheckVersioning(ctx); err != nil {
		return VersionID{}, err
	}
	var keyMarker string
	var versionMarker string
	seenMarkers := make(map[string]struct{})
	seenVersions := make(map[string]struct{})
	var exact []VersionID
	totalEntries := 0
	for page := 0; page < maxListPages; page++ {
		input := &s3.ListObjectVersionsInput{
			Bucket: ptrTo(store.bucket), Prefix: ptrTo(request.Key), MaxKeys: ptrTo(maxListKeys),
		}
		if keyMarker != "" {
			input.KeyMarker = ptrTo(keyMarker)
			input.VersionIdMarker = ptrTo(versionMarker)
		}
		output, err := store.client.ListObjectVersions(ctx, input)
		if err != nil || output == nil {
			return VersionID{}, ErrStorageUnavailable
		}
		pageEntries := len(output.Versions) + len(output.DeleteMarkers)
		totalEntries += pageEntries
		if pageEntries > int(maxListKeys) || totalEntries > maxListedEntries || len(output.CommonPrefixes) != 0 {
			return VersionID{}, ErrPaginationInvalid
		}
		if len(output.DeleteMarkers) != 0 {
			return VersionID{}, ErrReconcileAmbiguous
		}
		for _, candidate := range output.Versions {
			if candidate.Key == nil || *candidate.Key != request.Key ||
				candidate.VersionId == nil || candidate.Size == nil {
				return VersionID{}, ErrReconcileAmbiguous
			}
			version, versionErr := NewVersionID(*candidate.VersionId)
			if versionErr != nil {
				return VersionID{}, ErrReconcileAmbiguous
			}
			if _, duplicate := seenVersions[version.String()]; duplicate {
				return VersionID{}, ErrReconcileAmbiguous
			}
			seenVersions[version.String()] = struct{}{}
			if *candidate.Size != request.Metadata.SizeBytes {
				continue
			}
			_, getErr := store.getExact(ctx, ExactObjectRequest{
				Key: request.Key, VersionID: version, Metadata: request.Metadata,
			})
			if getErr == nil {
				exact = append(exact, version)
				if len(exact) > 1 {
					return VersionID{}, ErrReconcileAmbiguous
				}
				continue
			}
			if !errors.Is(getErr, ErrObjectMismatch) && !errors.Is(getErr, ErrManifestInvalid) {
				return VersionID{}, getErr
			}
		}
		if output.IsTruncated == nil {
			return VersionID{}, ErrPaginationInvalid
		}
		if !*output.IsTruncated {
			break
		}
		if page == maxListPages-1 {
			return VersionID{}, ErrPaginationLimit
		}
		if output.NextKeyMarker == nil || *output.NextKeyMarker == "" ||
			output.NextVersionIdMarker == nil || *output.NextVersionIdMarker == "" {
			return VersionID{}, ErrPaginationInvalid
		}
		nextPair := *output.NextKeyMarker + "\x00" + *output.NextVersionIdMarker
		if _, duplicate := seenMarkers[nextPair]; duplicate {
			return VersionID{}, ErrPaginationInvalid
		}
		seenMarkers[nextPair] = struct{}{}
		keyMarker = *output.NextKeyMarker
		versionMarker = *output.NextVersionIdMarker
	}
	if len(exact) != 1 {
		return VersionID{}, ErrReconcileUnresolved
	}
	return exact[0], nil
}

func prehashAndRewind(request PutRequest) (string, error) {
	if _, err := request.Body.Seek(0, io.SeekStart); err != nil {
		return "", ErrInvalidRequest
	}
	md5Hasher := md5.New()
	shaHasher := sha256.New()
	count, err := io.Copy(io.MultiWriter(md5Hasher, shaHasher), io.LimitReader(request.Body, request.Metadata.SizeBytes+1))
	if err != nil || count != request.Metadata.SizeBytes || hex.EncodeToString(shaHasher.Sum(nil)) != request.Metadata.SHA256 {
		return "", ErrObjectMismatch
	}
	if _, err := request.Body.Seek(0, io.SeekStart); err != nil {
		return "", ErrInvalidRequest
	}
	return base64.StdEncoding.EncodeToString(md5Hasher.Sum(nil)), nil
}

func objectMetadata(metadata ObjectMetadata) map[string]string {
	return map[string]string{
		metadataSHA256:             metadata.SHA256,
		metadataSizeBytes:          decimal(metadata.SizeBytes),
		metadataCapturedGeneration: decimal(metadata.CapturedGeneration),
		metadataAttemptSequence:    decimal(metadata.AttemptSequence),
		metadataBackupID:           metadata.BackupID,
		metadataManifestVersion:    decimal(metadata.ManifestVersion),
		metadataLeaseFence:         decimal(metadata.LeaseFence),
	}
}

func metadataMatches(actual map[string]string, expected ObjectMetadata) bool {
	if len(actual) != metadataFieldCount {
		return false
	}
	want := objectMetadata(expected)
	for key, value := range want {
		if actual[key] != value {
			return false
		}
	}
	return true
}

func isValidExactObjectRequest(request ExactObjectRequest) bool {
	return isValidObjectMetadata(request.Metadata) && validBoundKey(request.Key, request.Metadata) &&
		validVersionID(request.VersionID.String())
}

func isValidYandexS3Config(config YandexS3Config) bool {
	endpoint, err := url.ParseRequestURI(config.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil ||
		endpoint.Path != "" || endpoint.RawPath != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return false
	}
	return validRegion(config.Region) && validBucket(config.Bucket) &&
		config.AccessKeyID != "" && strings.TrimSpace(config.AccessKeyID) == config.AccessKeyID &&
		config.SecretAccessKey != "" &&
		strings.TrimSpace(config.SecretAccessKey) == config.SecretAccessKey
}

func validRegion(region string) bool {
	if len(region) < 2 || len(region) > 64 || !asciiLowerOrDigit(region[0]) || !asciiLowerOrDigit(region[len(region)-1]) {
		return false
	}
	for _, character := range region {
		if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-') {
			return false
		}
	}
	return true
}

func validBucket(bucket string) bool {
	if len(bucket) < 3 || len(bucket) > 63 || !asciiLowerOrDigit(bucket[0]) ||
		!asciiLowerOrDigit(bucket[len(bucket)-1]) || net.ParseIP(bucket) != nil ||
		strings.Contains(bucket, "..") || strings.Contains(bucket, ".-") || strings.Contains(bucket, "-.") {
		return false
	}
	for _, character := range bucket {
		if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' || character == '.') {
			return false
		}
	}
	return true
}

func asciiLowerOrDigit(character byte) bool {
	return (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')
}

func decimal(value int64) string {
	return strconv.FormatInt(value, 10)
}

type byteCounter struct {
	count int64
}

func (counter *byteCounter) Write(data []byte) (int, error) {
	counter.count += int64(len(data))
	return len(data), nil
}

func ptrTo[T any](value T) *T {
	return &value
}

func int64Ptr(value int64) *int64 {
	return &value
}

func valueOrZero[T any](value *T) T {
	if value == nil {
		var zero T
		return zero
	}
	return *value
}
