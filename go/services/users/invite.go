package users

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"ssv/go/app"
	"ssv/go/database/config"
	"ssv/go/services/crypto"
	"ssv/go/services/email"
	"time"

	"github.com/Data-Corruption/lmdb-go/lmdb"
	"github.com/Data-Corruption/stdx/xhttp"
	"github.com/Data-Corruption/stdx/xlog"
	"golang.org/x/time/rate"
)

const InviteMaxAgeHours = 12

// StartUserInvite creates a new user and emails them an invite link to set their password, and also
// serves as email verification. If an error occurs sending the email, the new user will not be saved.
func StartUserInvite(ctx context.Context, userEmail string, perms []string) error {
	if !email.IsAddressValid(userEmail) {
		return &xhttp.Err{Code: 400, Msg: "invalid email", Err: nil}
	}
	// get app data
	appData, ok := app.FromContext(ctx)
	if !ok {
		return &xhttp.Err{Code: 500, Msg: "failed to get app data", Err: nil}
	}
	// TODO validate perms
	db, userDBI, err := getUserDB(ctx)
	if err != nil {
		return err
	}
	return db.Update(func(txn *lmdb.Txn) error {
		// check if email already in use
		emailKey := emailToKey(userEmail)
		if _, err := txn.Get(userDBI, emailKey); err == nil {
			return &xhttp.Err{Code: 409, Msg: "email already in use", Err: nil}
		} else if !lmdb.IsNotFound(err) {
			return err
		}
		// gen invite token
		rawToken, err := crypto.GenRandomString(32)
		if err != nil {
			return err
		}
		prefix := []byte("invite.")
		hash := sha256.Sum256([]byte(rawToken))
		inviteKey := append(prefix, hash[:]...)
		// create user
		newUser := User{
			Perms:        perms,
			Email:        userEmail,
			CreatedAt:    time.Now().UTC(),
			InviteExpiry: time.Now().Add(InviteMaxAgeHours * time.Hour).UTC(),
			InviteKey:    inviteKey,
		}
		userBytes, err := json.Marshal(newUser)
		if err != nil {
			return err
		}
		// gen user ID
		var newUserID []byte
		for i := 0; i < 10; i++ {
			randStr, err := crypto.GenRandomString(16)
			if err != nil {
				return err
			}
			newUserID = append([]byte("user."), []byte(randStr)...)
			if _, err := txn.Get(userDBI, newUserID); lmdb.IsNotFound(err) {
				break
			}
		}
		// write user
		if err := txn.Put(userDBI, newUserID, userBytes, 0); err != nil {
			return err
		}
		// write email and invite index
		if err := txn.Put(userDBI, emailKey, newUserID, 0); err != nil {
			return err
		}
		if err := txn.Put(userDBI, inviteKey, newUserID, 0); err != nil {
			return err
		}
		// send invite email
		inviteLink := fmt.Sprintf("%sinvite?auth=%s", appData.UrlPrefix, rawToken)
		subject := "You've been invited to an SVLens instance!"
		body := fmt.Sprintf("You've been invited to an SVLens instance! Click the link below to create your account.\n\n%s\n\nNote: This invite expires after %d hours.", inviteLink, InviteMaxAgeHours)
		return email.SendEmail(ctx, userEmail, subject, body)
	})
}

// burst 5, sustained 5 req/s (12.5k attempts per our 12 hour window)
var inviteLimiter = rate.NewLimiter(rate.Every(200*time.Millisecond), 5)

// CompleteUserInvite completes the user invite process.
func CompleteUserInvite(ctx context.Context, token, username, password string) error {
	if token == "" {
		return &xhttp.Err{Code: 400, Msg: "invalid token", Err: nil}
	}
	if username == "" {
		return &xhttp.Err{Code: 400, Msg: "invalid username", Err: nil}
	}
	if password == "" {
		return &xhttp.Err{Code: 400, Msg: "invalid password", Err: nil}
	}
	// rate limit
	if !inviteLimiter.Allow() {
		return &xhttp.Err{Code: 429, Msg: "too many requests, try again later", Err: nil}
	}
	// get config values
	ppVersion, err := config.Get[int](ctx, "ppVersion")
	if err != nil {
		return fmt.Errorf("failed to get ppVersion from config: %w", err)
	}
	db, userDBI, err := getUserDB(ctx)
	if err != nil {
		return fmt.Errorf("failed to get user database: %w", err)
	}
	// hash password
	passHash, passSalt, err := crypto.HashPassword(password)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}
	expired := false
	err = db.Update(func(txn *lmdb.Txn) error {
		// calculate invite key
		prefix := []byte("invite.")
		hash := sha256.Sum256([]byte(token))
		inviteKey := append(prefix, hash[:]...)
		// get user key
		userKey, err := txn.Get(userDBI, inviteKey)
		if err != nil {
			if lmdb.IsNotFound(err) {
				return &xhttp.Err{Code: 404, Msg: "invite not found", Err: nil}
			}
			return err
		}
		// get user
		var user User
		if bytes, err := txn.Get(userDBI, userKey); err != nil {
			return fmt.Errorf("failed to get user by invite key: %w", err)
		} else if err := json.Unmarshal(bytes, &user); err != nil {
			return fmt.Errorf("failed to unmarshal user: %w", err)
		}
		// check if invite is still valid
		if user.InviteExpiry.Before(time.Now()) {
			expired = true
			// delete invite key and user
			if err := txn.Del(userDBI, inviteKey, nil); err != nil && !lmdb.IsNotFound(err) {
				xlog.Errorf(ctx, "failed to delete expired invite key: %s", err)
			}
			if err := txn.Del(userDBI, userKey, nil); err != nil && !lmdb.IsNotFound(err) {
				xlog.Errorf(ctx, "failed to delete expired user: %s", err)
			}
			return nil
		}
		// update user
		user.Name = username
		user.PassHash = passHash
		user.PassSalt = passSalt
		user.InviteKey = nil
		user.InviteExpiry = time.Time{}
		user.AgreedPP = ppVersion
		user.Notified = true // no need to notify of a policy update they just agreed to
		// write user
		updatedBytes, err := json.Marshal(user)
		if err != nil {
			return fmt.Errorf("failed to marshal updated user: %w", err)
		}
		if err := txn.Put(userDBI, userKey, updatedBytes, 0); err != nil {
			return fmt.Errorf("failed to save updated user: %w", err)
		}
		// delete invite key
		if err := txn.Del(userDBI, inviteKey, nil); err != nil && !lmdb.IsNotFound(err) {
			return fmt.Errorf("failed to delete invite key: %w", err)
		}
		return nil
	})
	if expired {
		return &xhttp.Err{Code: 400, Msg: "invite expired", Err: nil}
	}
	return err
}
