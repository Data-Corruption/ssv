package users

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/Data-Corruption/lmdb-go/lmdb"
)

// invalidateUserSessions removes all sessions associated with the provided user ID.
// It deletes both the individual session keys and the user session index entry.
func invalidateUserSessions(txn *lmdb.Txn, sessionDBI lmdb.DBI, userKey []byte) error {
	if len(userKey) == 0 {
		return fmt.Errorf("user ID is empty when invalidating sessions")
	}
	// calculate user session index key
	userHash := sha256.Sum256(userKey)
	indexKey := append([]byte("user_sessions."), userHash[:]...)
	// get session keys from index
	bytes, err := txn.Get(sessionDBI, indexKey)
	if err != nil {
		if lmdb.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to fetch session index for user %x: %w", userKey, err)
	}
	// delete each session
	if len(bytes) > 0 {
		var sessionKeys []string
		if err := json.Unmarshal(bytes, &sessionKeys); err != nil {
			return fmt.Errorf("failed to decode session index for user %x: %w", userKey, err)
		}
		for _, key := range sessionKeys {
			if key == "" {
				continue
			}
			if err := txn.Del(sessionDBI, []byte(key), nil); err != nil && !lmdb.IsNotFound(err) {
				return fmt.Errorf("failed to delete session %s for user %x: %w", key, userKey, err)
			}
		}
	}
	// delete index
	if err := txn.Del(sessionDBI, indexKey, nil); err != nil && !lmdb.IsNotFound(err) {
		return fmt.Errorf("failed to delete session index for user %x: %w", userKey, err)
	}
	return nil
}
