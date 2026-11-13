//go:build integration

package mskemulator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
)

const emulatorPort = "9297"

func emulatorHome() string {
	p, err := filepath.Abs(".emulator-home")
	if err != nil {
		panic(err)
	}
	return p
}

func repoRoot() string {
	p, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		panic(err)
	}
	return p
}

// emulatorEnv is the complete AWS_* environment the suite and every kcp it execs
// run with. AWS_CA_BUNDLE replaces the system roots with the emulator's CA, so it
// is also what makes any request that escapes AWS_ENDPOINT_URL fail its TLS
// handshake instead of reaching real AWS.
func emulatorEnv() map[string]string {
	dir := filepath.Join(emulatorHome(), ".aws", "emulator", emulatorPort)
	return map[string]string{
		"AWS_ENDPOINT_URL":                  "https://127.0.0.1:" + emulatorPort,
		"AWS_SHARED_CREDENTIALS_FILE":       filepath.Join(dir, "emulator_credentials"),
		"AWS_CONFIG_FILE":                   filepath.Join(dir, "config"),
		"AWS_CA_BUNDLE":                     filepath.Join(dir, "ca.pem"),
		"AWS_EC2_METADATA_DISABLED":         "true",
		"AWS_DISABLE_HOST_PREFIX_INJECTION": "true",
		"AWS_REGION":                        "us-east-1",
		"AWS_DEFAULT_REGION":                "us-east-1",
	}
}

// applyEmulatorEnv removes every AWS_* variable before setting emulatorEnv: the
// SDK reads many of them (static and legacy keys, web identity, per-service
// endpoint overrides) ahead of the emulator's shared credentials file, and would
// otherwise pick up a developer's or CI agent's real AWS identity.
func applyEmulatorEnv() error {
	env := emulatorEnv()
	if _, err := os.Stat(env["AWS_CA_BUNDLE"]); err != nil {
		return fmt.Errorf("AWS Emulator not running (%s missing); run the suite with `make test-msk-emulator`", env["AWS_CA_BUNDLE"])
	}

	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "AWS_") {
			if err := os.Unsetenv(k); err != nil {
				return err
			}
		}
	}
	for k, v := range env {
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	return nil
}

func sdkConfig(ctx context.Context, region string) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx, config.WithRegion(region))
}
