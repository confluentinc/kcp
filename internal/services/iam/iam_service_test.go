package iam

import (
	"testing"
)

func TestExtractPrincipalFromArn(t *testing.T) {
	t.Run("role principal", func(t *testing.T) {
		name, typ, err := extractPrincipalFromArn("arn:aws:iam::123456789000:role/mskRoleTestARN")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if name != "mskRoleTestARN" || typ != "role" {
			t.Fatalf("unexpected result: %s %s", name, typ)
		}
	})

	t.Run("user principal", func(t *testing.T) {
		name, typ, err := extractPrincipalFromArn("arn:aws:iam::123456789000:user/mskUserTestARN")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		
		if name != "mskUserTestARN" || typ != "user" {
			t.Fatalf("unexpected result: %s %s", name, typ)
		}
	})
}
