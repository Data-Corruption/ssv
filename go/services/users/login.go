package users

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"ssv/go/services/crypto"
	"time"

	"github.com/Data-Corruption/lmdb-go/lmdb"
	"github.com/Data-Corruption/stdx/xhttp"
)

const (
	MaxFailedLogins     = 5
	FailedLoginDuration = time.Hour
)

var (
	GenericLoginErr = &xhttp.Err{Code: 401, Msg: "invalid email or password", Err: nil}
	LoginLockoutErr = &xhttp.Err{Code: 403, Msg: "account locked due to too many failed login attempts, try again later", Err: errors.New("too many failed login attempts")}
)

// LoginUser checks the given email and password, returning the user key if successful.
// If the password is incorrect, it adds a failed login attempt.
// If the failed attempts exceeds MaxFailedLogins, it returns LoginLockoutErr.
func LoginUser(ctx context.Context, email, password string) ([]byte, error) {
	if email == "" {
		return nil, &xhttp.Err{Code: 400, Msg: "invalid email", Err: nil}
	}
	if password == "" {
		return nil, &xhttp.Err{Code: 400, Msg: "invalid password", Err: nil}
	}
	db, userDBI, err := getUserDB(ctx)
	if err != nil {
		return nil, err
	}
	var returnErr error = nil
	var userKey []byte
	err = db.Update(func(txn *lmdb.Txn) error {
		// get key by email
		userKey, err = txn.Get(userDBI, emailToKey(email))
		if err != nil {
			if lmdb.IsNotFound(err) {
				return GenericLoginErr
			}
		}
		if len(userKey) == 0 {
			return errors.New("empty user key for email " + email)
		}
		// get user
		var user User
		if bytes, err := txn.Get(userDBI, userKey); err != nil {
			return err
		} else if err := json.Unmarshal(bytes, &user); err != nil {
			return err
		}
		// remove old failed logins
		now := time.Now().UTC()
		var updatedFailedLogins []time.Time
		for _, t := range user.FailedLogins {
			if now.Sub(t) < FailedLoginDuration {
				updatedFailedLogins = append(updatedFailedLogins, t)
			}
		}
		if len(user.FailedLogins) < MaxFailedLogins { // prevent unbounded growth
			updatedFailedLogins = append(updatedFailedLogins, now)
		}
		user.FailedLogins = updatedFailedLogins
		// update user
		if updatedBytes, err := json.Marshal(user); err != nil {
			return fmt.Errorf("failed to encode user: %w", err)
		} else if err := txn.Put(userDBI, userKey, updatedBytes, 0); err != nil {
			return fmt.Errorf("failed to save user: %w", err)
		}
		// if too many failed logins, reject
		if len(user.FailedLogins) >= MaxFailedLogins {
			returnErr = LoginLockoutErr
			return nil // nil so we commit the txn
		}
		// check password
		if !crypto.ComparePasswords(password, user.PassHash, user.PassSalt) {
			returnErr = GenericLoginErr
		}
		return nil // nil so we commit the txn
	})
	if returnErr != nil {
		return userKey, returnErr
	}
	return userKey, err
}
