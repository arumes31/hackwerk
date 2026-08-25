package auth

import (
	"errors"
	"testing"
)

func TestRoleAllows(t *testing.T) {
	t.Parallel()
	if !RoleDriver.Allows(PermissionCalendarViewAll) {
		t.Fatal("driver must see all appointments")
	}
	if RoleDriver.Allows(PermissionAppointmentFix) {
		t.Fatal("driver must not fix appointments")
	}
	if Role("unknown").Allows(PermissionDashboardView) {
		t.Fatal("unknown role must be denied")
	}
	if err := (Actor{UserID: "driver", Role: RoleDriver}).Require(PermissionUserManage); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Require() error = %v", err)
	}
}

func TestPasswordHashVerifyAndRehash(t *testing.T) {
	t.Parallel()
	oldHasher, err := NewPasswordHasher(PasswordParameters{MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := oldHasher.Hash("Ein gutes Testpasswort 2026")
	if err != nil {
		t.Fatal(err)
	}
	valid, needsRehash, err := oldHasher.Verify("Ein gutes Testpasswort 2026", hash)
	if err != nil || !valid || needsRehash {
		t.Fatalf("Verify() = %v, %v, %v", valid, needsRehash, err)
	}
	newHasher, err := NewPasswordHasher(PasswordParameters{MemoryKiB: 16, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14})
	if err != nil {
		t.Fatal(err)
	}
	valid, needsRehash, err = newHasher.Verify("Ein gutes Testpasswort 2026", hash)
	if err != nil || !valid || !needsRehash {
		t.Fatalf("Verify upgraded = %v, %v, %v", valid, needsRehash, err)
	}
	valid, _, err = oldHasher.Verify("Falsches Testpasswort 2026", hash)
	if err != nil || valid {
		t.Fatalf("wrong password valid = %v, err = %v", valid, err)
	}
}

func TestPasswordPolicy(t *testing.T) {
	t.Parallel()
	hasher, err := NewPasswordHasher(PasswordParameters{MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14})
	if err != nil {
		t.Fatal(err)
	}
	if err := hasher.ValidatePassword("kurz"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("ValidatePassword() error = %v", err)
	}
}

func TestTokenHashStableAndRawDistinct(t *testing.T) {
	t.Parallel()
	token, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || string(TokenHash(token)) == token {
		t.Fatal("token or at-rest hash is invalid")
	}
	if len(TokenHash(token)) != 32 {
		t.Fatal("token hash has unexpected length")
	}
}
