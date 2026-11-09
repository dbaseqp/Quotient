package db

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CompetitionStateSchema is a singleton: every reader and writer uses row
// competitionStateID, which migrate normalizes legacy rows to.
type CompetitionStateSchema struct {
	ID        uint `gorm:"primarykey"`
	Started   bool
	StartedAt *time.Time
	// OvertimeEnd is the competition end an admin resumed scoring after.
	OvertimeEnd *time.Time
}

const competitionStateID = 1

// competitionState reads the singleton row, which migrate seeds.
func (d *DB) competitionState() (CompetitionStateSchema, error) {
	var state CompetitionStateSchema
	err := d.db.Where("id = ?", competitionStateID).First(&state).Error
	return state, err
}

// updateCompetitionState locks the singleton row, applies fn and saves every
// column. fn runs in the same transaction. All access goes through this and
// competitionState; migrate normalizes and seeds the row.
func (d *DB) updateCompetitionState(fn func(tx *gorm.DB, state *CompetitionStateSchema) error) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		var state CompetitionStateSchema
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", competitionStateID).First(&state).Error; err != nil {
			return err
		}
		if err := fn(tx, &state); err != nil {
			return err
		}
		return tx.Save(&state).Error
	})
}

// initCompetitionState moves a legacy state row created with an auto-assigned
// id onto competitionStateID, then seeds the row if there is none.
func (d *DB) initCompetitionState() error {
	if err := d.db.Exec(`UPDATE competition_state_schemas SET id = ?
		WHERE id = (SELECT MIN(id) FROM competition_state_schemas)
		AND NOT EXISTS (SELECT 1 FROM competition_state_schemas WHERE id = ?)`,
		competitionStateID, competitionStateID).Error; err != nil {
		return err
	}
	return d.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&CompetitionStateSchema{ID: competitionStateID}).Error
}

func (d *DB) GetCompetitionStarted() bool {
	state, err := d.competitionState()
	if err != nil {
		return false
	}
	return state.Started
}

func (d *DB) SetCompetitionStarted(started bool) error {
	return d.updateCompetitionState(func(tx *gorm.DB, state *CompetitionStateSchema) error {
		state.Started = started
		if !started || state.StartedAt != nil {
			return nil
		}
		now := time.Now()
		state.StartedAt = &now
		return recalculateImportedInjectTimes(tx, now)
	})
}

func (d *DB) GetCompetitionStart() (*time.Time, error) {
	state, err := d.competitionState()
	if err != nil {
		return nil, err
	}
	return state.StartedAt, nil
}

func (d *DB) GetOvertimeEnd() (time.Time, error) {
	state, err := d.competitionState()
	if err != nil || state.OvertimeEnd == nil {
		return time.Time{}, err
	}
	return *state.OvertimeEnd, nil
}

// SetOvertimeEnd records the end an admin resumed scoring after; nil forgets it.
func (d *DB) SetOvertimeEnd(end *time.Time) error {
	return d.updateCompetitionState(func(_ *gorm.DB, state *CompetitionStateSchema) error {
		state.OvertimeEnd = end
		return nil
	})
}
