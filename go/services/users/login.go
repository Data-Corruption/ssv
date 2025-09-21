package users

import (
	"context"
	"encoding/json"
	"errors"
	"ssv/go/services/crypto"
	"time"

	"github.com/Data-Corruption/lmdb-go/lmdb"
	"github.com/Data-Corruption/stdx/xhttp"
)

const MaxHourlyFailedLogins = 5

var GenericLoginErr = &xhttp.Err{Code: 401, Msg: "invalid email or password", Err: errors.New("user not found")}
var LoginLockoutErr = &xhttp.Err{Code: 403, Msg: "account locked due to too many failed login attempts, try again later", Err: errors.New("too many failed login attempts")}

// LoginUser checks the given email and password, returning the user ID if successful.
// If the password is incorrect, it adds a failed login attempt.
// If the failed attempts exceeds MaxHourlyFailedLogins, it returns LoginLockoutErr.
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
	var id []byte
	err = db.Update(func(txn *lmdb.Txn) error {
		// get id by email
		id, err = txn.Get(userDBI, emailToKey(email))
		if err != nil {
			if lmdb.IsNotFound(err) {
				return GenericLoginErr
			}
		}
		if len(id) == 0 {
			return errors.New("empty user ID for email " + email)
		}
		// get user
		var user User
		if bytes, err := txn.Get(userDBI, id); err != nil {
			return err
		} else if err := json.Unmarshal(bytes, &user); err != nil {
			return err
		}
		// check password / happy path
		if crypto.ComparePasswords(password, user.PassHash, user.PassSalt) {
			return nil
		}
		// password incorrect, add failed login, remove old failed logins
		currentTime := time.Now()
		var updatedFailedLogins []time.Time
		for _, t := range user.LoginFails {
			if currentTime.Sub(t) < time.Hour {
				updatedFailedLogins = append(updatedFailedLogins, t)
			}
		}
		if len(user.LoginFails) < MaxHourlyFailedLogins { // prevent unbounded growth
			updatedFailedLogins = append(updatedFailedLogins, currentTime)
		}
		user.LoginFails = updatedFailedLogins
		// save user with updated failed logins
		if updatedBytes, err := json.Marshal(user); err != nil {
			return err
		} else if err := txn.Put(userDBI, id, updatedBytes, 0); err != nil {
			return err
		}
		// if too many failed logins, reject
		if len(user.LoginFails) >= MaxHourlyFailedLogins {
			returnErr = LoginLockoutErr
		}
		returnErr = GenericLoginErr
		return nil // nil so we commit the txn
	})
	if returnErr != nil {
		return id, returnErr
	}
	return id, err
}
