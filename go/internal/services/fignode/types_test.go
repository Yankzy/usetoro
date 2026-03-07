package fignode

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestFloatConversions(t *testing.T) {
	t.Run("numericToFloat64", func(t *testing.T) {
		n := float64ToNumeric(123.456)
		f := numericToFloat64(n)
		if f != 123.46 { // rounded to 2 decimal places in standard helper
			t.Errorf("expected 123.46, got %v", f)
		}
	})

	t.Run("numericToFloat64Precise", func(t *testing.T) {
		n := float64ToNumeric(123.4567)
		f := numericToFloat64Precise(n)
		if f != 123.457 { // rounded to 3 decimal places
			t.Errorf("expected 123.457, got %v", f)
		}
	})

	t.Run("invalid numeric", func(t *testing.T) {
		n := pgtype.Numeric{Valid: false}
		f := numericToFloat64(n)
		if f != 0 {
			t.Errorf("expected 0, got %v", f)
		}
	})
}

func TestPasswordHashing(t *testing.T) {
	pwd := "supersecret"
	hash := hashPassword(pwd)

	if hash == pwd {
		t.Error("hash shouldn't equal plaintext")
	}

	if !checkPassword(pwd, hash) {
		t.Error("password check failed for valid password")
	}

	if checkPassword("wrong", hash) {
		t.Error("password check succeeded for invalid password")
	}
}
