package users

// for migration, use the config package, bump that version and use the migration funcs there.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"ssv/go/database"
	"ssv/go/services/crypto"
	"strings"
	"time"

	"github.com/Data-Corruption/lmdb-go/lmdb"
	"github.com/Data-Corruption/lmdb-go/wrap"
)

type User struct {
	ID        []byte   `json:"-"` // no need to store key
	Perms     []string `json:"perms"`
	Name      string   `json:"name"`
	Email     string   `json:"email"`
	EditEmail string   `json:"editEmail"` // candidate email for email change
	AgreedPP  int      `json:"agreedPP"`  // version of privacy policy the user agreed to
	Notified  bool     `json:"notified"`  // whether the user has been notified of a privacy policy update
	PassSalt  string   `json:"passSalt"`
	PassHash  string   `json:"passHash"`
	// times are in UTC
	CreatedAt       time.Time   `json:"createdAt"`
	FailedLogins    []time.Time `json:"failedLogins"` // times of failed login attempts
	InviteExpiry    time.Time   `json:"inviteExpiry"`
	EmailEditExpiry time.Time   `json:"emailEditExpiry"`
	PassEditExpiry  time.Time   `json:"passEditExpiry"`
	// for cleanup
	EmailKey     []byte `json:"emailKey"`
	InviteKey    []byte `json:"inviteKey"`
	EmailEditKey []byte `json:"emailEditKey"`
	PassEditKey  []byte `json:"passEditKey"`
}

// helper for funcs doing txns
func getUserDB(ctx context.Context) (*wrap.DB, lmdb.DBI, error) {
	db := database.FromContext(ctx)
	if db == nil {
		return nil, 0, errors.New("failed to get database from context")
	}
	return db, db.GetDBis()[database.UserDBIName], nil
}

func emailToKey(email string) []byte {
	prefix := []byte("email.")
	hash := sha256.Sum256([]byte(strings.ToLower(email)))
	return append(prefix, hash[:]...)
}

// GetAllUsers iterates keys with prefix "user." in the given DBI and returns all users.
// It fails fast on any LMDB or JSON error (explicit, error-surfacing behavior).
func GetAllUsers(ctx context.Context) ([]User, error) {
	db, userDBI, err := getUserDB(ctx)
	if err != nil {
		return nil, err
	}

	var out []User
	err = db.View(func(txn *lmdb.Txn) error {
		cur, err := txn.OpenCursor(userDBI)
		if err != nil {
			return err
		}
		defer cur.Close()

		prefix := []byte("user.")
		k, v, err := cur.Get(prefix, nil, lmdb.SetRange)
		for ; err == nil && bytes.HasPrefix(k, prefix); k, v, err = cur.Get(nil, nil, lmdb.Next) {
			var u User
			if err := json.Unmarshal(v, &u); err != nil {
				return fmt.Errorf("unmarshal %q: %w", string(k), err) // include the key for debug
			}
			// add ID from key, redact secrets
			u.ID = k
			u.PassSalt = ""
			u.PassHash = ""
			out = append(out, u)
		}
		if lmdb.IsNotFound(err) {
			return nil
		}
		return err
	})

	return out, err
}

// GetUserByKey retrieves a user by their key.
// Use lmdb.IsNotFound(err) to check if the user was not found.
func GetUserByKey(ctx context.Context, userKey []byte) (*User, error) {
	db := database.FromContext(ctx)
	if db == nil {
		return nil, errors.New("failed to get database from context")
	}
	var user User
	if bytes, err := db.Read(database.UserDBIName, userKey); err != nil {
		return nil, err
	} else if err := json.Unmarshal(bytes, &user); err != nil {
		return nil, err
	}
	// add id, redact secrets
	user.ID = userKey
	user.PassSalt = ""
	user.PassHash = ""
	return &user, nil
}

// GetUserByEmail retrieves a user by their email.
func GetUserByEmail(ctx context.Context, email string) (*User, error) {
	db := database.FromContext(ctx)
	if db == nil {
		return nil, errors.New("failed to get database from context")
	}
	if bytes, err := db.Read(database.UserDBIName, emailToKey(email)); err != nil {
		return nil, err
	} else if len(bytes) != 0 {
		return GetUserByKey(ctx, bytes)
	}
	return nil, fmt.Errorf("user with email %s not found", email)
}

func RemoveUser(ctx context.Context, userKey []byte) error {
	db, userDBI, err := getUserDB(ctx)
	if err != nil {
		return err
	}
	return db.Update(func(txn *lmdb.Txn) error {
		// get user
		var user User
		if bytes, err := txn.Get(userDBI, userKey); err != nil {
			return err
		} else if err := json.Unmarshal(bytes, &user); err != nil {
			return err
		}
		if len(user.EmailKey) == 0 {
			return fmt.Errorf("user email key is empty: %x", userKey)
		}
		// delete email -> id mapping
		if err := txn.Del(userDBI, user.EmailKey, nil); err != nil && !lmdb.IsNotFound(err) {
			return fmt.Errorf("failed to delete email key for user %x: %w", userKey, err)
		}
		// delete other keys if they != 0
		if len(user.InviteKey) > 0 {
			if err := txn.Del(userDBI, user.InviteKey, nil); err != nil && !lmdb.IsNotFound(err) {
				return fmt.Errorf("failed to delete invite key for user %x: %w", userKey, err)
			}
		}
		if len(user.EmailEditKey) > 0 {
			if err := txn.Del(userDBI, user.EmailEditKey, nil); err != nil && !lmdb.IsNotFound(err) {
				return fmt.Errorf("failed to delete email edit key for user %x: %w", userKey, err)
			}
		}
		if len(user.PassEditKey) > 0 {
			if err := txn.Del(userDBI, user.PassEditKey, nil); err != nil && !lmdb.IsNotFound(err) {
				return fmt.Errorf("failed to delete pass edit key for user %x: %w", userKey, err)
			}
		}
		// delete user
		if err := txn.Del(userDBI, userKey, nil); err != nil {
			return fmt.Errorf("failed to delete user %x: %w", userKey, err)
		}
		return nil
	})
}

// SetUserPerms sets the given user's permissions.
func SetUserPerms(ctx context.Context, userKey []byte, perms []string) error {
	// TODO validate perms?
	db, userDBI, err := getUserDB(ctx)
	if err != nil {
		return err
	}
	return db.Update(func(txn *lmdb.Txn) error {
		// get user
		var user User
		if bytes, err := txn.Get(userDBI, userKey); err != nil {
			return err
		} else if err := json.Unmarshal(bytes, &user); err != nil {
			return err
		}
		// set perms
		user.Perms = perms
		// save
		if updatedBytes, err := json.Marshal(user); err != nil {
			return err
		} else if err := txn.Put(userDBI, userKey, updatedBytes, 0); err != nil {
			return err
		}
		return nil
	})
}

// SetUserEmail sets the given user's email, updating the email -> id mapping as well.
// Use with caution, this bypasses email verification and is intended for admin use only.
func SetUserEmail(ctx context.Context, userKey []byte, newEmail string) error {
	db, userDBI, err := getUserDB(ctx)
	if err != nil {
		return err
	}
	return db.Update(func(txn *lmdb.Txn) error {
		// get user
		var user User
		if bytes, err := txn.Get(userDBI, userKey); err != nil {
			return err
		} else if err := json.Unmarshal(bytes, &user); err != nil {
			return err
		}
		// delete old email mapping
		if err := txn.Del(userDBI, user.EmailKey, nil); err != nil && !lmdb.IsNotFound(err) {
			return fmt.Errorf("failed to delete old email key for user %x: %w", userKey, err)
		}
		// set new email
		user.Email = newEmail
		user.EmailKey = emailToKey(newEmail)
		// save user
		if updatedBytes, err := json.Marshal(user); err != nil {
			return err
		} else if err := txn.Put(userDBI, userKey, updatedBytes, 0); err != nil {
			return err
		}
		// create new email mapping
		if err := txn.Put(userDBI, user.EmailKey, userKey, 0); err != nil {
			return err
		}
		return nil
	})
}

// ResetUserFailedLogins clears the user's failed login attempts.
func ResetUserFailedLogins(ctx context.Context, userKey []byte) error {
	db, userDBI, err := getUserDB(ctx)
	if err != nil {
		return err
	}
	return db.Update(func(txn *lmdb.Txn) error {
		// get user
		var user User
		if bytes, err := txn.Get(userDBI, userKey); err != nil {
			return err
		} else if err := json.Unmarshal(bytes, &user); err != nil {
			return err
		}
		// reset failed logins
		user.FailedLogins = []time.Time{}
		// save
		if updatedBytes, err := json.Marshal(user); err != nil {
			return err
		} else if err := txn.Put(userDBI, userKey, updatedBytes, 0); err != nil {
			return err
		}
		return nil
	})
}

// TODO implement
func ExportUserData(ctx context.Context, userKey []byte) (string, error) {
	// Will need to get user struct, omitting auth tokens and such.
	// Also will need to zip all cached user data. That will be a lot so I'll need to probs do a multi-part tar.gz
	// and provide it as a download link behind basic session auth.
	return "", errors.New("not implemented")
}

// genKey generates a unique token key with the given prefix and length
// tries up to 10 times to get a unique key, returns error if it fails
// if hash is true, the token is hashed with sha256 before being used as key
func genKey(txn *lmdb.Txn, dbi lmdb.DBI, prefix string, tokenLength int, hash bool) ([]byte, string, error) {
	var key []byte
	var token string
	var err error
	isUnique := false
	for i := 0; i < 10; i++ {
		token, err = crypto.GenRandomString(tokenLength)
		if err != nil {
			return nil, "", err
		}
		if hash {
			hash := sha256.Sum256([]byte(token))
			key = append([]byte(prefix), hash[:]...)
		} else {
			key = append([]byte(prefix), []byte(token)...)
		}
		if _, err := txn.Get(dbi, key); lmdb.IsNotFound(err) {
			isUnique = true
			break
		}
	}
	if !isUnique {
		return nil, "", fmt.Errorf("failed to generate unique token key")
	}
	return key, token, nil
}
