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
	"ssv/go/services/email"

	"github.com/Data-Corruption/lmdb-go/lmdb"
	"github.com/Data-Corruption/stdx/xhttp"
	"golang.org/x/time/rate"
)

const EmailEditMaxAgeMinutes = 15

// StartEmailEdit records a pending email change for the user and emails the verification link.
func StartEmailEdit(ctx context.Context, userKey []byte, emailCandidate string) error {
	if len(userKey) == 0 {
		return &xhttp.Err{Code: 400, Msg: "invalid user", Err: nil}
	}
	if !email.IsAddressValid(emailCandidate) {
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
		// lazy check if candidate email in use
		candidateKey := emailToKey(emailCandidate)
		if _, err := txn.Get(userDBI, candidateKey); err == nil {
			return &xhttp.Err{Code: 409, Msg: "email already in use", Err: nil}
		} else if !lmdb.IsNotFound(err) {
			return fmt.Errorf("failed to check candidate email: %w", err)
		}
		// get user
		var user User
		if bytes, err := txn.Get(userDBI, userKey); err != nil {
			return fmt.Errorf("failed to fetch user: %w", err)
		} else if err := json.Unmarshal(bytes, &user); err != nil {
			return fmt.Errorf("failed to decode user: %w", err)
		}
		// lazy check if same email
		if strings.EqualFold(user.Email, emailCandidate) {
			return &xhttp.Err{Code: 400, Msg: "this is already your current email", Err: nil}
		}
		// if already pending email change, delete that attempt's auth key
		if len(user.EmailEditKey) > 0 {
			if err := txn.Del(userDBI, user.EmailEditKey, nil); err != nil && !lmdb.IsNotFound(err) {
				return fmt.Errorf("failed to delete email edit key: %w", err)
			}
		}
		// generate email edit key
		emailEditKey, token, err := genKey(txn, userDBI, "email_edit.", 32, true)
		if err != nil {
			return fmt.Errorf("failed to generate email edit key: %w", err)
		}
		// update user
		user.EditEmail = emailCandidate
		user.EmailEditKey = append([]byte{}, emailEditKey...)
		user.EmailEditExpiry = time.Now().UTC().Add(EmailEditMaxAgeMinutes * time.Minute)
		if updatedBytes, err := json.Marshal(user); err != nil {
			return fmt.Errorf("failed to encode user: %w", err)
		} else if err := txn.Put(userDBI, userKey, updatedBytes, 0); err != nil {
			return fmt.Errorf("failed to save user: %w", err)
		}
		// store email edit key
		if err := txn.Put(userDBI, emailEditKey, userKey, 0); err != nil {
			return fmt.Errorf("failed to store email edit token: %w", err)
		}
		// send email
		editLink := fmt.Sprintf("%semail-edit?auth=%s", appData.UrlPrefix, token)
		subject := "SVLens Email Verification"
		body := fmt.Sprintf("You've requested to edit your email. Click the link below to verify your new email. If this was not requested by you, please ignore this.\n\n%s\n\nNote: This link expires after %d minutes.", editLink, EmailEditMaxAgeMinutes)
		return email.SendEmail(ctx, emailCandidate, subject, body)
	})
}

var emailEditLimiter = rate.NewLimiter(rate.Every(200*time.Millisecond), 5)

// CompleteEmailEdit finalizes a pending email change if the provided token is valid.
func CompleteEmailEdit(ctx context.Context, token string) error {
	if token == "" {
		return &xhttp.Err{Code: 400, Msg: "invalid token", Err: nil}
	}
	// rate limit
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := emailEditLimiter.Wait(ctx); err != nil {
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
	// start txn
	var returnErr error // var to hold errors that should not abort txn
	err = db.Update(func(txn *lmdb.Txn) error {
		// calculate email edit key from token
		hash := sha256.Sum256([]byte(token))
		emailKey := append([]byte("email_edit."), hash[:]...)
		// get user key from email edit key
		userKey, err := txn.Get(userDBI, emailKey)
		if err != nil {
			if lmdb.IsNotFound(err) {
				return &xhttp.Err{Code: 404, Msg: "email verification token not found", Err: nil}
			}
			return fmt.Errorf("failed to look up email edit token: %w", err)
		}
		// delete email edit key
		if err := txn.Del(userDBI, emailKey, nil); err != nil && !lmdb.IsNotFound(err) {
			return fmt.Errorf("failed to delete email edit key: %w", err)
		}
		// get user
		var user User
		if bytes, err := txn.Get(userDBI, userKey); err != nil {
			return fmt.Errorf("failed to fetch user: %w", err)
		} else if err := json.Unmarshal(bytes, &user); err != nil {
			return fmt.Errorf("failed to decode user: %w", err)
		}

		// check stuff, if anything is wrong, set returnErr, if set skip email change but reset edit fields

		// check if no candidate email or email edit key
		if user.EditEmail == "" || user.EmailEditKey == nil {
			returnErr = &xhttp.Err{Code: 500, Msg: "Whoops, something went wrong", Err: fmt.Errorf("user %x has no pending email change", userKey)}
		}
		// check token not expired
		now := time.Now().UTC()
		if user.EmailEditExpiry.Before(now) || user.EmailEditExpiry.IsZero() {
			returnErr = &xhttp.Err{Code: 400, Msg: "email verification token expired", Err: nil}
		}
		// check candidate email not in use
		var newEmailKey []byte
		if user.EditEmail != "" {
			newEmailKey = emailToKey(user.EditEmail)
			if _, err := txn.Get(userDBI, newEmailKey); err == nil {
				returnErr = &xhttp.Err{Code: 409, Msg: "email already in use", Err: nil}
			} else if !lmdb.IsNotFound(err) {
				return fmt.Errorf("failed to check email uniqueness: %w", err)
			}
		}

		// if no issues, perform email change
		if returnErr == nil {
			// delete old email mapping, write new mapping, invalidate sessions
			if err := txn.Del(userDBI, user.EmailKey, nil); err != nil && !lmdb.IsNotFound(err) {
				return fmt.Errorf("failed to delete old email mapping: %w", err)
			}
			if err := txn.Put(userDBI, newEmailKey, userKey, 0); err != nil {
				return fmt.Errorf("failed to store new email mapping: %w", err)
			}
			if err := invalidateUserSessions(txn, sessionDBI, userKey); err != nil {
				return err
			}
			// set new email and email key on user
			user.Email = user.EditEmail
			user.EmailKey = append([]byte{}, newEmailKey...)
		}

		// clear pending email change fields and save user
		user.EditEmail = ""
		user.EmailEditKey = nil
		user.EmailEditExpiry = time.Time{}
		if updatedBytes, err := json.Marshal(user); err != nil {
			return fmt.Errorf("failed to encode user: %w", err)
		} else if err := txn.Put(userDBI, userKey, updatedBytes, 0); err != nil {
			return fmt.Errorf("failed to persist user %x: %w", userKey, err)
		}
		return nil
	})
	if returnErr != nil {
		return returnErr
	}
	return err
}
