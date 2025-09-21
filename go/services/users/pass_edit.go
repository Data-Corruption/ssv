package users

import (
	"fmt"
	"net/http"
	"ssv/internal/app/info"
	"ssv/internal/app/models"
	"svlens/internal/app/repository"
	"svlens/internal/utils"
	"time"

	"gorm.io/gorm"
)

// StartPasswordEdit sets the pass edit token, and expiry for the user, then sends an email to the user.
func StartPasswordEdit(email string) error {
	utils.Config.Mutex.RLock()
	defer utils.Config.Mutex.RUnlock()
	if !IsEmailConfigured() {
		return ErrEmailNotConfigured
	}
	if !IsEmailValid(email) {
		return utils.NewHttpErr(http.StatusBadRequest, nil, "invalid email")
	}
	// gen token
	newToken, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("failed to generate pass edit token: %w", err)
	}
	// prepare updates
	updates := map[string]interface{}{
		"pass_edit_token":  &newToken,
		"pass_edit_expiry": time.Now().Add(time.Duration(utils.Config.Server.AuthTokenDurMins) * time.Minute),
	}
	// update user and send email
	return repository.DB.Transaction(func(tx *gorm.DB) error {
		// update user
		result := tx.Model(&models.User{}).Where("email = ?", email).Updates(updates)
		if err := HandleGormWriteError(fmt.Errorf("failed to start password edit for user with email %s", email), result, true); err != nil {
			return err
		}
		// send email
		editLink := fmt.Sprintf("%s/password-edit?auth=%s", info.Server.GetBaseURL(), newToken.String())
		subject := "SVLens Password Reset"
		body := fmt.Sprintf("You've requested to reset your password. Click the link below to reset your password. If this was not requested by you, please ignore this.\n\n%s\n\nNote: This link expires after %d minutes.", editLink, utils.Config.Server.AuthTokenDurMins)
		return SendEmail(email, subject, body)
	})
}

// CompletePasswordEdit
func CompletePasswordEdit(token uuid.UUID, newPassword string) error {
	if token == uuid.Nil {
		return utils.NewHttpErr(http.StatusBadRequest, nil, "invalid token")
	}
	// get user, ensure pass edit is in progress and not expired
	var user models.User
	if err := repository.DB.Where("pass_edit_token = ?", token).First(&user).Error; err != nil {
		return HandleGormReadError(fmt.Errorf("failed to complete password edit with token %s", token), err)
	}
	if user.PassEditToken == nil || user.PassEditExpiry.Before(time.Now()) {
		return utils.NewHttpErr(http.StatusBadRequest, nil, "expired token, please request a new password edit from your account")
	}
	// check if token matches
	if *user.PassEditToken != token {
		return utils.NewHttpErr(http.StatusBadRequest, nil, "invalid token")
	}
	// prepare updates
	updates := map[string]interface{}{
		"pass_hash":        "",
		"pass_salt":        "",
		"pass_edit_token":  nil,
		"pass_edit_expiry": time.Time{},
	}
	var err error
	if updates["pass_hash"], updates["pass_salt"], err = utils.HashPassword(newPassword); err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}
	// delete user sessions
	if result := repository.DB.Where("user_id = ?", user.ID).Delete(&models.Session{}); result.Error != nil {
		return HandleGormWriteError(fmt.Errorf("failed to complete password edit with token %s", token), result, false)
	}
	// update user
	result := repository.DB.Model(&models.User{}).Where("id = ?", user.ID).Updates(updates)
	return HandleGormWriteError(fmt.Errorf("failed to complete password edit with token %s", token), result, true)
}
