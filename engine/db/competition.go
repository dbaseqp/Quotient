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
	// ScheduledStart and ScheduledEnd are the runtime schedule; when either
	// is set they replace the event.conf schedule as a whole.
	ScheduledStart *time.Time
	ScheduledEnd   *time.Time
	// OvertimeEnd is the competition end (configured or scheduled) an admin
	// resumed scoring after.
	OvertimeEnd *time.Time
}

const competitionStateID = 1

// RuntimeScheduled reports whether a runtime schedule replaces event.conf's.
func (s CompetitionStateSchema) RuntimeScheduled() bool {
	return s.ScheduledStart != nil || s.ScheduledEnd != nil
}

// GetCompetitionState reads the singleton row, which migrate seeds.
func (d *DB) GetCompetitionState() (CompetitionStateSchema, error) {
	var state CompetitionStateSchema
	err := d.db.Where("id = ?", competitionStateID).First(&state).Error
	return state, err
}

// updateCompetitionState locks the singleton row, applies fn in the same
// transaction and saves every column.
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
	state, err := d.GetCompetitionState()
	if err != nil {
		return false
	}
	return state.Started
}

// SetCompetitionStarted opens or closes the competition; the first start is
// recorded as of now and re-anchors imported injects to it.
func (d *DB) SetCompetitionStarted(started bool) error {
	return d.updateCompetitionState(func(tx *gorm.DB, state *CompetitionStateSchema) error {
		return state.start(tx, started, time.Now())
	})
}

// StartCompetitionOnce opens the competition as of its scheduled start unless
// it has ever been started; it reports whether it did.
func (d *DB) StartCompetitionOnce(at time.Time) (bool, error) {
	var started bool
	err := d.updateCompetitionState(func(tx *gorm.DB, state *CompetitionStateSchema) error {
		if state.StartedAt != nil {
			return nil
		}
		started = true
		return state.start(tx, true, at)
	})
	return started, err
}

func (state *CompetitionStateSchema) start(tx *gorm.DB, started bool, at time.Time) error {
	state.Started = started
	if !started || state.StartedAt != nil {
		return nil
	}
	state.StartedAt = &at
	return recalculateImportedInjectTimes(tx, at)
}

func (d *DB) GetCompetitionStart() (*time.Time, error) {
	state, err := d.GetCompetitionState()
	if err != nil {
		return nil, err
	}
	return state.StartedAt, nil
}

// SetCompetitionSchedule stores the runtime schedule (both nil clears it);
// effectiveStart/effectiveEnd are the schedule in force afterwards: a changed
// end forgets overtime and, before the first start, imported injects re-anchor
// to effectiveStart (noStart: there is none).
func (d *DB) SetCompetitionSchedule(start, end, effectiveStart, effectiveEnd *time.Time) (noStart bool, err error) {
	err = d.updateCompetitionState(func(tx *gorm.DB, state *CompetitionStateSchema) error {
		state.ScheduledStart, state.ScheduledEnd = start, end
		state.forgetOvertimeUnless(effectiveEnd)
		if state.StartedAt != nil {
			return nil
		}
		if effectiveStart == nil {
			noStart = true
			return nil
		}
		return recalculateImportedInjectTimes(tx, *effectiveStart)
	})
	return noStart, err
}

// forgetOvertimeUnless drops OvertimeEnd unless end is the end it was granted for.
func (state *CompetitionStateSchema) forgetOvertimeUnless(end *time.Time) {
	if state.OvertimeEnd != nil && (end == nil || !end.Equal(*state.OvertimeEnd)) {
		state.OvertimeEnd = nil
	}
}

// SetOvertimeEnd records the end an admin resumed scoring after; nil forgets it.
func (d *DB) SetOvertimeEnd(end *time.Time) error {
	return d.updateCompetitionState(func(_ *gorm.DB, state *CompetitionStateSchema) error {
		state.OvertimeEnd = end
		return nil
	})
}
