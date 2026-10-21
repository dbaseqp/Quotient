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

// SeedOriginalCredentials syncs original and unrotated team credentials with the credlist records.
func (d *DB) SeedOriginalCredentials(credlistName string, credentials [][]string) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		usernames := make([]string, 0, len(credentials))
		for _, cred := range credentials {
			if len(cred) != 2 {
				continue
			}
			username, password := cred[0], cred[1]
			usernames = append(usernames, username)

			orig := OriginalCredentialSchema{
				CredlistName: credlistName,
				Username:     username,
				Password:     password,
			}
			if err := tx.Where("credlist_name = ? AND username = ?", credlistName, username).FirstOrCreate(&orig).Error; err != nil {
				return err
			}
			if orig.Password != password {
				oldPassword := orig.Password
				orig.Password = password
				if err := tx.Save(&orig).Error; err != nil {
					return err
				}
				var unrotated []CredentialSchema
				if err := tx.Where("credlist_name = ? AND username = ? AND password = ?", credlistName, username, oldPassword).Find(&unrotated).Error; err != nil {
					return err
				}
				for _, tc := range unrotated {
					history := PCRHistorySchema{
						TeamID:       tc.TeamID,
						CredlistName: credlistName,
						Username:     username,
						OldPassword:  oldPassword,
						NewPassword:  password,
						ChangedBy:    "system",
					}
					if err := tx.Create(&history).Error; err != nil {
						return err
					}
				}
				if err := tx.Model(&CredentialSchema{}).
					Where("credlist_name = ? AND username = ? AND password = ?", credlistName, username, oldPassword).
					Update("password", password).Error; err != nil {
					return err
				}
			}
		}

		if len(usernames) == 0 {
			if err := tx.Where("credlist_name = ?", credlistName).Delete(&OriginalCredentialSchema{}).Error; err != nil {
				return err
			}
			return tx.Where("credlist_name = ?", credlistName).Delete(&CredentialSchema{}).Error
		}
		if err := tx.Where("credlist_name = ? AND username NOT IN ?", credlistName, usernames).Delete(&OriginalCredentialSchema{}).Error; err != nil {
			return err
		}
		return tx.Where("credlist_name = ? AND username NOT IN ?", credlistName, usernames).Delete(&CredentialSchema{}).Error
	})
}

// SeedTeamCredentials copies original credentials to a team's credential set
func (d *DB) SeedTeamCredentials(teamID uint) error {
	var originals []OriginalCredentialSchema
	if err := d.db.Find(&originals).Error; err != nil {
		return err
	}
	for _, orig := range originals {
		cred := CredentialSchema{
			TeamID:       teamID,
			CredlistName: orig.CredlistName,
			Username:     orig.Username,
			Password:     orig.Password,
		}
		result := d.db.Where("team_id = ? AND credlist_name = ? AND username = ?", teamID, orig.CredlistName, orig.Username).FirstOrCreate(&cred)
		if result.Error != nil {
			return result.Error
		}
	}
	return nil
}

// GetTeamCredentials returns all credentials for a team and credlist
func (d *DB) GetTeamCredentials(teamID uint, credlistName string) ([]CredentialSchema, error) {
	var creds []CredentialSchema
	result := d.db.Where("team_id = ? AND credlist_name = ?", teamID, credlistName).Find(&creds)
	if result.Error != nil {
		return nil, result.Error
	}
	return creds, nil
}

// UpdateCredential updates a single credential and logs to history
func (d *DB) UpdateCredential(teamID uint, credlistName, username, newPassword, changedBy string) error {
	var cred CredentialSchema
	result := d.db.Where("team_id = ? AND credlist_name = ? AND username = ?", teamID, credlistName, username).First(&cred)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return errors.New("credential not found")
		}
		return result.Error
	}

	oldPassword := cred.Password

	cred.Password = newPassword
	if err := d.db.Save(&cred).Error; err != nil {
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
	return d.db.Create(&history).Error
}

// ResetTeamCredlist resets a team's credlist to original values
func (d *DB) ResetTeamCredlist(teamID uint, credlistName, changedBy string) error {
	var currentCreds []CredentialSchema
	if err := d.db.Where("team_id = ? AND credlist_name = ?", teamID, credlistName).Find(&currentCreds).Error; err != nil {
		return err
	}

	var originals []OriginalCredentialSchema
	if err := d.db.Where("credlist_name = ?", credlistName).Find(&originals).Error; err != nil {
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
		if err := d.db.Create(&history).Error; err != nil {
			return err
		}

		cred.Password = origPassword
		if err := d.db.Save(&cred).Error; err != nil {
			return err
		}
	}

	return nil
}

// GetPCRHistory returns history for a team/credlist, optionally filtered by username
func (d *DB) GetPCRHistory(teamID uint, credlistName string, username string) ([]PCRHistorySchema, error) {
	var history []PCRHistorySchema
	query := d.db.Where("team_id = ? AND credlist_name = ?", teamID, credlistName)
	if username != "" {
		query = query.Where("username = ?", username)
	}
	result := query.Order("changed_at DESC").Find(&history)
	if result.Error != nil {
		return nil, result.Error
	}
	return history, nil
}

// GetOriginalCredentials returns original credentials for a credlist
func (d *DB) GetOriginalCredentials(credlistName string) ([]OriginalCredentialSchema, error) {
	var creds []OriginalCredentialSchema
	result := d.db.Where("credlist_name = ?", credlistName).Find(&creds)
	if result.Error != nil {
		return nil, result.Error
	}
	return creds, nil
}
