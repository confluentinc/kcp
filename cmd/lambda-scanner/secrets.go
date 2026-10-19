package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/smithy-go"
)

const secretReadTimeout = 30 * time.Second

// secretsAPI is the subset of the Secrets Manager client the handler uses (injectable for tests).
type secretsAPI interface {
	GetSecretValue(ctx context.Context, in *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

// secretsClientFunc returns a client for the given region.
type secretsClientFunc func(ctx context.Context, region string) (secretsAPI, error)

// defaultSecretsClient builds a client from the Lambda process's own environment, i.e. the
// execution role's credentials. These are never passed to kcp.
func defaultSecretsClient(ctx context.Context, region string) (secretsAPI, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return secretsmanager.NewFromConfig(cfg), nil
}

// secretCreds is the content of a {"username","password"} secret, or the error from reading it.
type secretCreds struct {
	username, password string
	err                error
}

// parseSecretArn validates arn:aws:secretsmanager:<region>:<account>:secret:<name> and returns the region.
func parseSecretArn(arn string) (string, error) {
	p := strings.SplitN(arn, ":", 7)
	if len(p) != 7 || p[0] != "arn" || !strings.HasPrefix(p[1], "aws") || p[2] != "secretsmanager" ||
		p[3] == "" || p[4] == "" || p[5] != "secret" || p[6] == "" {
		return "", errors.New("secret ARN must look like arn:aws:secretsmanager:<region>:<account>:secret:<name>")
	}
	return p[3], nil
}

// regionClient is a lazily built client for one region, shared by all ARNs in that region so the
// AWS config is loaded once per region.
type regionClient struct {
	once   sync.Once
	client secretsAPI
	err    error
}

// readSecrets reads each distinct ARN once, in parallel, sharing one secretReadTimeout across all
// reads so unreachable ARNs cannot add up. Errors and values stay in the returned map; the caller
// fails the steps that need a secret whose read failed.
func (h *handler) readSecrets(ctx context.Context, arns []string) map[string]secretCreds {
	out := map[string]secretCreds{}
	newClient := h.secretsClient
	if newClient == nil {
		newClient = defaultSecretsClient
	}
	ctx, cancel := context.WithTimeout(ctx, secretReadTimeout)
	defer cancel()

	clients := map[string]*regionClient{}
	regions := map[string]string{} // arn -> region
	for _, arn := range arns {
		if _, done := regions[arn]; done {
			continue
		}
		region, err := parseSecretArn(arn)
		if err != nil {
			out[arn] = secretCreds{err: err}
			regions[arn] = ""
			continue
		}
		regions[arn] = region
		if clients[region] == nil {
			clients[region] = &regionClient{}
		}
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	for arn, region := range regions {
		if region == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			rc := clients[region]
			rc.once.Do(func() { rc.client, rc.err = newClient(ctx, region) })
			var sc secretCreds
			if rc.err != nil {
				sc = secretCreds{err: fmt.Errorf("failed to read secret %s: %s", arn, awsErrText(rc.err))}
			} else {
				sc = readSecret(ctx, rc.client, arn)
			}
			mu.Lock()
			out[arn] = sc
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

func readSecret(ctx context.Context, client secretsAPI, arn string) secretCreds {
	res, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: &arn})
	if err != nil {
		return secretCreds{err: fmt.Errorf("failed to read secret %s: %s", arn, awsErrText(err))}
	}
	if res.SecretString == nil {
		return secretCreds{err: fmt.Errorf("secret %s has no SecretString", arn)}
	}
	var v struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	// json errors can quote input; do not wrap them.
	if json.Unmarshal([]byte(*res.SecretString), &v) != nil {
		return secretCreds{err: fmt.Errorf("secret %s is not a JSON object", arn)}
	}
	if v.Username == "" || v.Password == "" {
		return secretCreds{err: fmt.Errorf(`secret %s must be JSON with non-empty "username" and "password"`, arn)}
	}
	return secretCreds{username: v.Username, password: v.Password}
}

// awsErrText renders an AWS error as "<code>: <message>" without anything request-specific.
func awsErrText(err error) string {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		return ae.ErrorCode() + ": " + ae.ErrorMessage()
	}
	return err.Error()
}
