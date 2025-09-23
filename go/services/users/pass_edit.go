package users

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ssv/go/app"
	"ssv/go/database"
	"ssv/go/services/crypto"
	"ssv/go/services/email"

	"github.com/Data-Corruption/lmdb-go/lmdb"
	"github.com/Data-Corruption/stdx/xhttp"
	"golang.org/x/time/rate"
)

const PassEditMaxAgeMinutes = 15

// StartPasswordEdit generates a reset token, stores it for the user, and emails the reset link.
func StartPasswordEdit(ctx context.Context, userEmail string) error {
	if !email.IsAddressValid(userEmail) {
		return &xhttp.Err{Code: 400, Msg: "invalid email", Err: nil}
	}
	appData, ok := app.FromContext(ctx)
	if !ok {
		return &xhttp.Err{Code: 500, Msg: "failed to get app data", Err: nil}
	}
	db, userDBI, err := getUserDB(ctx)
	if err != nil {
		return err
	}
	return db.Update(func(txn *lmdb.Txn) error {
		// get user key by email
		userKey, err := txn.Get(userDBI, emailToKey(userEmail))
		if err != nil {
			if lmdb.IsNotFound(err) {
				return &xhttp.Err{Code: 404, Msg: "user not found", Err: nil}
			}
			return fmt.Errorf("failed to look up user by email: %w", err)
		}
		if len(userKey) == 0 {
			return fmt.Errorf("empty user ID for email %s", userEmail)
		}
		// get user
		var user User
		if bytes, err := txn.Get(userDBI, userKey); err != nil {
			return fmt.Errorf("failed to fetch user: %w", err)
		} else if err := json.Unmarshal(bytes, &user); err != nil {
			return fmt.Errorf("failed to decode user: %w", err)
		}
		// if already pending password reset, delete that attempt's auth key
		if len(user.PassEditKey) > 0 {
			if err := txn.Del(userDBI, user.PassEditKey, nil); err != nil && !lmdb.IsNotFound(err) {
				return fmt.Errorf("failed to delete existing password edit key: %w", err)
			}
		}
		// generate password edit key
		passEditKey, token, err := genKey(txn, userDBI, "password_edit.", 32, true)
		if err != nil {
			return fmt.Errorf("failed to generate password edit key: %w", err)
		}
		// update user
		user.PassEditKey = append([]byte{}, passEditKey...)
		user.PassEditExpiry = time.Now().UTC().Add(PassEditMaxAgeMinutes * time.Minute)
		if updatedBytes, err := json.Marshal(user); err != nil {
			return fmt.Errorf("failed to encode user: %w", err)
		} else if err := txn.Put(userDBI, userKey, updatedBytes, 0); err != nil {
			return fmt.Errorf("failed to save user: %w", err)
		}
		// store pass edit key
		if err := txn.Put(userDBI, passEditKey, userKey, 0); err != nil {
			return fmt.Errorf("failed to store password edit token: %w", err)
		}
		// send email
		editLink := fmt.Sprintf("%spassword-edit?auth=%s", appData.UrlPrefix, token)
		subject := fmt.Sprintf("%s Password Reset", strings.ToUpper(appData.Name))
		body := fmt.Sprintf("You've requested to reset your password. Click the link below to reset your password. If this was not requested by you, please ignore this.\n\n%s\n\nNote: This link expires after %d minutes.", editLink, PassEditMaxAgeMinutes)
		return email.SendEmail(ctx, userEmail, subject, body)
	})
}

var passEditLimiter = rate.NewLimiter(rate.Every(200*time.Millisecond), 5)

// CompletePasswordEdit finalizes the password reset, updating credentials if the token is valid.
func CompletePasswordEdit(ctx context.Context, token, newPassword string) error {
	if token == "" {
		return &xhttp.Err{Code: 400, Msg: "invalid token", Err: nil}
	}
	if newPassword == "" {
		return &xhttp.Err{Code: 400, Msg: "invalid password", Err: nil}
	}
	// rate limit
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := passEditLimiter.Wait(ctx); err != nil {
		return &xhttp.Err{Code: 429, Msg: "too many requests, try again later", Err: err}
	}
	// get db stuff
	db, userDBI, err := getUserDB(ctx)
	if err != nil {
		return err
	}
	sessionDBI, ok := db.GetDBis()[database.SessionDBIName]
	if !ok {
		return fmt.Errorf("session DBI not found")
	}
	// hash new password
	passHash, passSalt, err := crypto.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}
	// start txn
	var returnErr error // var to hold errors that should not abort txn
	err = db.Update(func(txn *lmdb.Txn) error {
		// calculate pass edit key from token
		hash := sha256.Sum256([]byte(token))
		passKey := append([]byte("password_edit."), hash[:]...)
		// get user key from pass edit key
		userKey, err := txn.Get(userDBI, passKey)
		if err != nil {
			if lmdb.IsNotFound(err) {
				return &xhttp.Err{Code: 404, Msg: "password reset token not found", Err: nil}
			}
			return fmt.Errorf("failed to look up password reset token: %w", err)
		}
		// delete pass edit key
		if err := txn.Del(userDBI, passKey, nil); err != nil && !lmdb.IsNotFound(err) {
			return fmt.Errorf("failed to delete password edit key: %w", err)
		}
		// get user
		var user User
		if bytes, err := txn.Get(userDBI, userKey); err != nil {
			return fmt.Errorf("failed to fetch user: %w", err)
		} else if err := json.Unmarshal(bytes, &user); err != nil {
			return fmt.Errorf("failed to decode user: %w", err)
		}

		// check stuff, if anything is wrong, set returnErr, if set skip password change but reset edit fields

		// check if no pass edit key
		if user.PassEditKey == nil {
			returnErr = &xhttp.Err{Code: 500, Msg: "Whoops, something went wrong", Err: fmt.Errorf("user %x has no pending password reset", userKey)}
		}
		// check token not expired
		now := time.Now().UTC()
		if user.PassEditExpiry.Before(now) || user.PassEditExpiry.IsZero() {
			returnErr = &xhttp.Err{Code: 400, Msg: "password reset token expired", Err: nil}
		}

		// if no issues, perform password change
		if returnErr == nil {
			// set new password, invalidate sessions
			user.PassHash = passHash
			user.PassSalt = passSalt
			if err := invalidateUserSessions(txn, sessionDBI, userKey); err != nil {
				return err
			}
		}

		// save user
		user.PassEditKey = nil
		user.PassEditExpiry = time.Time{}
		if updatedBytes, err := json.Marshal(user); err != nil {
			return fmt.Errorf("failed to encode user: %w", err)
		} else if err := txn.Put(userDBI, userKey, updatedBytes, 0); err != nil {
			return fmt.Errorf("failed to save user: %w", err)
		}
		return nil
	})
	if returnErr != nil {
		return returnErr
	}
	return err
}
