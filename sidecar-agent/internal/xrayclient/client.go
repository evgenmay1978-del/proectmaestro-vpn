package xrayclient

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/vless"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	apiAddress           = "127.0.0.1:18082"
	commercialAPIAddress = "127.0.0.1:28082"
	managedInbound       = "maestro-cdn-in"
)

var managedEmail = regexp.MustCompile(`^wl:[^:]+:exit-s[1-4]$`)
var canonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type CredentialSource interface {
	Credential(context.Context, string) (string, error)
}

type Config struct {
	Address        string
	ServerName     string
	ClientCertFile string
	ClientKeyFile  string
	CAFile         string
}

type handlerRPC interface {
	GetInboundUsers(context.Context, *command.GetInboundUserRequest, ...grpc.CallOption) (*command.GetInboundUserResponse, error)
	AlterInbound(context.Context, *command.AlterInboundRequest, ...grpc.CallOption) (*command.AlterInboundResponse, error)
}

type Client struct {
	handler     handlerRPC
	credentials CredentialSource
	connection  *grpc.ClientConn
}

type DirectoryCredentials struct {
	Directory string
}

func New(config Config, source CredentialSource) (*Client, error) {
	if config.Address == "" {
		config.Address = apiAddress
	}
	if !validAPIAddress(config.Address) || source == nil {
		return nil, errors.New("xray client: invalid isolated API configuration")
	}
	tlsConfig, err := loadTLSConfig(config)
	if err != nil {
		return nil, err
	}
	connection, err := grpc.NewClient(config.Address, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	if err != nil {
		return nil, errors.New("xray client: create HandlerService client")
	}
	return &Client{handler: command.NewHandlerServiceClient(connection), credentials: source, connection: connection}, nil
}

func validAPIAddress(value string) bool {
	return value == apiAddress || value == commercialAPIAddress
}

func newWithHandler(handler handlerRPC, source CredentialSource) (*Client, error) {
	if handler == nil || source == nil {
		return nil, errors.New("xray client: invalid HandlerService client")
	}
	return &Client{handler: handler, credentials: source}, nil
}

func (client *Client) Close() error {
	if client == nil || client.connection == nil {
		return nil
	}
	return client.connection.Close()
}

func (client *Client) ListUsers(ctx context.Context, inboundTag string) ([]string, error) {
	if client == nil || client.handler == nil || inboundTag != managedInbound {
		return nil, errors.New("xray client: invalid managed inbound")
	}
	response, err := client.handler.GetInboundUsers(ctx, &command.GetInboundUserRequest{Tag: inboundTag})
	if err != nil {
		return nil, errors.New("xray client: HandlerService list users failed")
	}
	users := make([]string, 0, len(response.GetUsers()))
	seen := make(map[string]struct{}, len(response.GetUsers()))
	for _, user := range response.GetUsers() {
		if user == nil || user.Email == "" {
			return nil, errors.New("xray client: HandlerService returned invalid user")
		}
		if _, duplicate := seen[user.Email]; duplicate {
			return nil, errors.New("xray client: HandlerService returned duplicate user")
		}
		seen[user.Email] = struct{}{}
		users = append(users, user.Email)
	}
	return users, nil
}

func (client *Client) ManagedUserAccountMatches(ctx context.Context, inboundTag, email string) (bool, error) {
	if client == nil || client.handler == nil || client.credentials == nil || inboundTag != managedInbound ||
		!managedEmail.MatchString(email) {
		return false, errors.New("xray client: invalid managed account query")
	}
	credential, err := client.credentials.Credential(ctx, email)
	if err != nil || !canonicalUUID.MatchString(credential) {
		return false, errors.New("xray client: managed credential unavailable")
	}
	expected := serial.ToTypedMessage(&vless.Account{Id: credential, Encryption: "none"})
	response, err := client.handler.GetInboundUsers(ctx, &command.GetInboundUserRequest{Tag: inboundTag})
	if err != nil {
		return false, errors.New("xray client: HandlerService account query failed")
	}
	found := false
	matches := false
	for _, user := range response.GetUsers() {
		if user == nil || user.Email == "" {
			return false, errors.New("xray client: HandlerService returned invalid user")
		}
		if user.Email != email {
			continue
		}
		if found {
			return false, errors.New("xray client: HandlerService returned duplicate user")
		}
		found = true
		matches = typedMessagesEqual(user.Account, expected)
	}
	return found && matches, nil
}

func typedMessagesEqual(left, right *serial.TypedMessage) bool {
	if left == nil || right == nil {
		return false
	}
	leftDigest := sha256.Sum256(append(append([]byte(left.GetType()), 0), left.GetValue()...))
	rightDigest := sha256.Sum256(append(append([]byte(right.GetType()), 0), right.GetValue()...))
	return subtle.ConstantTimeCompare(leftDigest[:], rightDigest[:]) == 1
}

func (client *Client) AddUser(ctx context.Context, inboundTag, email string) error {
	if client == nil || client.handler == nil || client.credentials == nil || inboundTag != managedInbound ||
		!managedEmail.MatchString(email) {
		return errors.New("xray client: refusing unsafe user add")
	}
	credential, err := client.credentials.Credential(ctx, email)
	if err != nil || !canonicalUUID.MatchString(credential) {
		return errors.New("xray client: managed credential unavailable")
	}
	operation := &command.AddUserOperation{User: &protocol.User{
		Email: email,
		Account: serial.ToTypedMessage(&vless.Account{
			Id: credential, Encryption: "none",
		}),
	}}
	_, err = client.handler.AlterInbound(ctx, &command.AlterInboundRequest{
		Tag: inboundTag, Operation: serial.ToTypedMessage(operation),
	})
	if err != nil {
		return errors.New("xray client: HandlerService add user failed")
	}
	return nil
}

func (client *Client) RemoveUser(ctx context.Context, inboundTag, email string) error {
	if client == nil || client.handler == nil || inboundTag != managedInbound || !managedEmail.MatchString(email) {
		return errors.New("xray client: refusing unsafe user removal")
	}
	_, err := client.handler.AlterInbound(ctx, &command.AlterInboundRequest{
		Tag: inboundTag, Operation: serial.ToTypedMessage(&command.RemoveUserOperation{Email: email}),
	})
	if err != nil {
		return errors.New("xray client: HandlerService remove user failed")
	}
	return nil
}

func (source DirectoryCredentials) Credential(_ context.Context, email string) (string, error) {
	if !filepath.IsAbs(source.Directory) || !managedEmail.MatchString(email) {
		return "", errors.New("xray client: invalid credential source")
	}
	digest := sha256.Sum256([]byte(email))
	path := filepath.Join(source.Directory, hex.EncodeToString(digest[:])+".credential")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() < 1 || info.Size() > 256 {
		return "", errors.New("xray client: protected credential unavailable")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("xray client: protected credential unavailable")
	}
	credential := strings.TrimSpace(string(raw))
	if !canonicalUUID.MatchString(credential) {
		return "", errors.New("xray client: protected credential invalid")
	}
	return credential, nil
}

func loadTLSConfig(config Config) (*tls.Config, error) {
	if config.ServerName == "" || config.ClientCertFile == "" || config.ClientKeyFile == "" || config.CAFile == "" {
		return nil, errors.New("xray client: incomplete mTLS configuration")
	}
	certificate, err := tls.LoadX509KeyPair(config.ClientCertFile, config.ClientKeyFile)
	if err != nil {
		return nil, errors.New("xray client: load client certificate")
	}
	caBytes, err := os.ReadFile(config.CAFile)
	if err != nil {
		return nil, errors.New("xray client: load API CA")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caBytes) {
		return nil, errors.New("xray client: parse API CA")
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13, ServerName: config.ServerName,
		Certificates: []tls.Certificate{certificate}, RootCAs: roots, NextProtos: []string{"h2"},
	}, nil
}
