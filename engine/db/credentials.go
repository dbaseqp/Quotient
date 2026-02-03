package db

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// OriginalCredentialSchema stores the original/default credentials for each credlist.
// Shared across all teams. Used as source of truth for resets.
type OriginalCredentialSchema struct {
	ID           uint   `gorm:"primaryKey"`
	CredlistName string `gorm:"uniqueIndex:idx_orig_credlist_user;not null"`
	Username     string `gorm:"uniqueIndex:idx_orig_credlist_user;not null"`
	Password     string `gorm:"not null"`
}

// CredentialSchema stores current credential state per team.
// This is what the scoring engine uses for checks.
type CredentialSchema struct {
	ID           uint      `gorm:"primaryKey"`
	TeamID       uint      `gorm:"uniqueIndex:idx_team_credlist_user;not null"`
	CredlistName string    `gorm:"uniqueIndex:idx_team_credlist_user;not null"`
	Username     string    `gorm:"uniqueIndex:idx_team_credlist_user;not null"`
	Password     string    `gorm:"not null"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime"`
}

// PCRHistorySchema stores full audit trail of every credential change.
type PCRHistorySchema struct {
	ID           uint      `gorm:"primaryKey"`
	TeamID       uint      `gorm:"index;not null"`
	CredlistName string    `gorm:"not null"`
	Username     string    `gorm:"not null"`
	OldPassword  string    // empty for initial seeding
	NewPassword  string    `gorm:"not null"`
	ChangedBy    string    // username: "team3", "admin", or "system"
	ChangedAt    time.Time `gorm:"autoCreateTime"`
}

// SeedOriginalCredentials inserts original credentials from config (called once on first startup)
func SeedOriginalCredentials(credlistName string, credentials [][]string) error {
	for _, cred := range credentials {
		if len(cred) != 2 {
			continue
		}
		orig := OriginalCredentialSchema{
			CredlistName: credlistName,
			Username:     cred[0],
			Password:     cred[1],
		}
		result := db.Where("credlist_name = ? AND username = ?", credlistName, cred[0]).FirstOrCreate(&orig)
		if result.Error != nil {
			return result.Error
		}
	}
	return nil
}

// SeedTeamCredentials copies original credentials to a team's credential set
func SeedTeamCredentials(teamID uint) error {
	var originals []OriginalCredentialSchema
	if err := db.Find(&originals).Error; err != nil {
		return err
	}
	for _, orig := range originals {
		cred := CredentialSchema{
			TeamID:       teamID,
			CredlistName: orig.CredlistName,
			Username:     orig.Username,
			Password:     orig.Password,
		}
		result := db.Where("team_id = ? AND credlist_name = ? AND username = ?", teamID, orig.CredlistName, orig.Username).FirstOrCreate(&cred)
		if result.Error != nil {
			return result.Error
		}
	}
	return nil
}

// GetTeamCredentials returns all credentials for a team and credlist
func GetTeamCredentials(teamID uint, credlistName string) ([]CredentialSchema, error) {
	var creds []CredentialSchema
	result := db.Where("team_id = ? AND credlist_name = ?", teamID, credlistName).Find(&creds)
	if result.Error != nil {
		return nil, result.Error
	}
	return creds, nil
}

// GetAllTeamCredentials returns all credentials for a team (all credlists)
func GetAllTeamCredentials(teamID uint) ([]CredentialSchema, error) {
	var creds []CredentialSchema
	result := db.Where("team_id = ?", teamID).Find(&creds)
	if result.Error != nil {
		return nil, result.Error
	}
	return creds, nil
}

// UpdateCredential updates a single credential and logs to history
func UpdateCredential(teamID uint, credlistName, username, newPassword, changedBy string) error {
	var cred CredentialSchema
	result := db.Where("team_id = ? AND credlist_name = ? AND username = ?", teamID, credlistName, username).First(&cred)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return errors.New("credential not found")
		}
		return result.Error
	}

	oldPassword := cred.Password

	cred.Password = newPassword
	if err := db.Save(&cred).Error; err != nil {
		return err
	}

	history := PCRHistorySchema{
		TeamID:       teamID,
		CredlistName: credlistName,
		Username:     username,
		OldPassword:  oldPassword,
		NewPassword:  newPassword,
		ChangedBy:    changedBy,
	}
	return db.Create(&history).Error
}

// ResetTeamCredlist resets a team's credlist to original values
func ResetTeamCredlist(teamID uint, credlistName, changedBy string) error {
	var currentCreds []CredentialSchema
	if err := db.Where("team_id = ? AND credlist_name = ?", teamID, credlistName).Find(&currentCreds).Error; err != nil {
		return err
	}

	var originals []OriginalCredentialSchema
	if err := db.Where("credlist_name = ?", credlistName).Find(&originals).Error; err != nil {
		return err
	}

	origMap := make(map[string]string)
	for _, orig := range originals {
		origMap[orig.Username] = orig.Password
	}

	for _, cred := range currentCreds {
		origPassword, exists := origMap[cred.Username]
		if !exists {
			continue
		}

		history := PCRHistorySchema{
			TeamID:       teamID,
			CredlistName: credlistName,
			Username:     cred.Username,
			OldPassword:  cred.Password,
			NewPassword:  origPassword,
			ChangedBy:    changedBy,
		}
		if err := db.Create(&history).Error; err != nil {
			return err
		}

		cred.Password = origPassword
		if err := db.Save(&cred).Error; err != nil {
			return err
		}
	}

	return nil
}

// GetPCRHistory returns history for a team/credlist, optionally filtered by username
func GetPCRHistory(teamID uint, credlistName string, username string) ([]PCRHistorySchema, error) {
	var history []PCRHistorySchema
	query := db.Where("team_id = ? AND credlist_name = ?", teamID, credlistName)
	if username != "" {
		query = query.Where("username = ?", username)
	}
	result := query.Order("changed_at DESC").Find(&history)
	if result.Error != nil {
		return nil, result.Error
	}
	return history, nil
}

// IsCredentialsSeeded checks if original credentials have been seeded
func IsCredentialsSeeded() (bool, error) {
	var count int64
	if err := db.Model(&OriginalCredentialSchema{}).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetOriginalCredentials returns original credentials for a credlist
func GetOriginalCredentials(credlistName string) ([]OriginalCredentialSchema, error) {
	var creds []OriginalCredentialSchema
	result := db.Where("credlist_name = ?", credlistName).Find(&creds)
	if result.Error != nil {
		return nil, result.Error
	}
	return creds, nil
}
