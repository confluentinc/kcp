//go:build integration

package mskemulator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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

// applyEmulatorEnv points this process, and every kcp it execs, at the emulator
// only. The credential and profile variables are removed because the SDK prefers
// them over the shared credentials file, which would send a developer's or CI
// agent's real AWS credentials.
func applyEmulatorEnv() error {
	dir := filepath.Join(emulatorHome(), ".aws", "emulator", emulatorPort)
	ca := filepath.Join(dir, "ca.pem")
	if _, err := os.Stat(ca); err != nil {
		return fmt.Errorf("AWS Emulator not running (%s missing); run the suite with `make test-msk-emulator`", ca)
	}

	for _, k := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE", "AWS_DEFAULT_PROFILE"} {
		if err := os.Unsetenv(k); err != nil {
			return err
		}
	}
	for k, v := range map[string]string{
		"AWS_ENDPOINT_URL":                  "https://127.0.0.1:" + emulatorPort,
		"AWS_SHARED_CREDENTIALS_FILE":       filepath.Join(dir, "emulator_credentials"),
		"AWS_CONFIG_FILE":                   filepath.Join(dir, "config"),
		"AWS_CA_BUNDLE":                     ca,
		"AWS_EC2_METADATA_DISABLED":         "true",
		"AWS_DISABLE_HOST_PREFIX_INJECTION": "true",
		"AWS_REGION":                        "us-east-1",
		"AWS_DEFAULT_REGION":                "us-east-1",
	} {
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	return nil
}

func sdkConfig(ctx context.Context, region string) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx, config.WithRegion(region))
}
