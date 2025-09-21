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

// StartEmailEdit sets the candidate, email edit token, and expiry for the user, then sends an email to the new email.
func StartEmailEdit(userID uint, emailCandidate string) error {
	utils.Config.Mutex.RLock()
	defer utils.Config.Mutex.RUnlock()
	if !IsEmailConfigured() {
		return ErrEmailNotConfigured
	}
	if !IsEmailValid(emailCandidate) {
		return utils.NewHttpErr(http.StatusBadRequest, nil, "invalid email")
	}
	if err := userExists(userID); err != nil {
		return err
	}
	// count users with the new email to see if it's taken
	var count int64
	if err := repository.DB.Model(&models.User{}).Where("email = ?", emailCandidate).Count(&count).Error; err != nil {
		return HandleGormReadError(fmt.Errorf("failed to start email edit for user with ID %d", userID), err)
	}
	if count > 0 {
		return utils.NewHttpErr(http.StatusConflict, nil, "email already in use")
	}
	// get user
	var user models.User
	if err := repository.DB.Where("id = ?", userID).First(&user).Error; err != nil {
		return HandleGormReadError(fmt.Errorf("failed to start email edit for user with ID %d", userID), err)
	}
	// gen token
	newToken, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("failed to generate email edit token: %w", err)
	}
	// prepare updates
	updates := map[string]interface{}{
		"email_edit_email":  emailCandidate,
		"email_edit_token":  &newToken,
		"email_edit_expiry": time.Now().Add(time.Duration(utils.Config.Server.AuthTokenDurMins) * time.Minute),
	}
	// update user and send email
	return repository.DB.Transaction(func(tx *gorm.DB) error {
		// update user
		result := tx.Model(&models.User{}).Where("id = ?", userID).Updates(updates)
		if err := HandleGormWriteError(fmt.Errorf("failed to start email edit for user with ID %d", userID), result, true); err != nil {
			return err
		}
		// send email
		editLink := fmt.Sprintf("%s/email-edit?auth=%s", info.Server.GetBaseURL(), newToken.String())
		subject := "SVLens Email Verification"
		body := fmt.Sprintf("You've requested to edit your email. Click the link below to verify your new email. If this was not requested by you, please ignore this.\n\n%s\n\nNote: This link expires after %d minutes.", editLink, utils.Config.Server.AuthTokenDurMins)
		return SendEmail(emailCandidate, subject, body)
	})
}

// CompleteEmailEdit
func CompleteEmailEdit(token uuid.UUID) error {
	if token == uuid.Nil {
		return utils.NewHttpErr(http.StatusBadRequest, nil, "invalid token")
	}
	// get user, ensure email edit is in progress and not expired
	var user models.User
	if err := repository.DB.Where("email_edit_token = ?", token).First(&user).Error; err != nil {
		return HandleGormReadError(fmt.Errorf("failed to complete email edit with token %s", token), err)
	}
	if user.EmailEditToken == nil || user.EmailEditExpiry.Before(time.Now()) {
		return utils.NewHttpErr(http.StatusBadRequest, nil, "expired token, please request a new email edit from your account")
	}
	// check if token matches
	if *user.EmailEditToken != token {
		return utils.NewHttpErr(http.StatusBadRequest, nil, "invalid token")
	}
	// prepare updates
	updates := map[string]interface{}{
		"email":             user.EmailEditEmail,
		"email_edit_email":  "",
		"email_edit_token":  nil,
		"email_edit_expiry": time.Time{},
	}
	// delete user sessions
	if result := repository.DB.Where("user_id = ?", user.ID).Delete(&models.Session{}); result.Error != nil {
		return HandleGormWriteError(fmt.Errorf("failed to complete password edit with token %s", token), result, false)
	}
	// update user
	result := repository.DB.Model(&models.User{}).Where("id = ?", user.ID).Updates(updates)
	return HandleGormWriteError(fmt.Errorf("failed to complete email edit with token %s", token), result, true)
}
