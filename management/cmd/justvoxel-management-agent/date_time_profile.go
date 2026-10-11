package main

import (
	"encoding/json"
	"errors"
	"fmt"
)

type dateTimeProfile struct {
	TimeFormat string `json:"time_format"`
	DateFormat string `json:"date_format"`
}

func validDateTimeProfile(profile dateTimeProfile) bool {
	return (profile.TimeFormat == "12" || profile.TimeFormat == "24") &&
		(profile.DateFormat == "mdy" || profile.DateFormat == "dmy")
}

func (s *webUIStore) readDateTimeProfile() (dateTimeProfile, error) {
	var payload string
	if err := s.db.QueryRow(`SELECT profile_json FROM date_time_profile WHERE id = 1`).Scan(&payload); err != nil {
		return dateTimeProfile{}, fmt.Errorf("read date and time display profile: %w", err)
	}
	var profile dateTimeProfile
	if err := json.Unmarshal([]byte(payload), &profile); err != nil {
		return dateTimeProfile{}, fmt.Errorf("decode date and time display profile: %w", err)
	}
	if !validDateTimeProfile(profile) {
		return dateTimeProfile{}, errors.New("stored date and time display profile is invalid")
	}
	return profile, nil
}

func (s *webUIStore) writeDateTimeProfile(profile dateTimeProfile) error {
	if !validDateTimeProfile(profile) {
		return errors.New("date and time display profile is invalid")
	}
	payload, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("encode date and time display profile: %w", err)
	}
	result, err := s.db.Exec(
		`UPDATE date_time_profile SET profile_json = ?, updated_at = ? WHERE id = 1`,
		string(payload),
		formatWebUIStoreTime(s.now()),
	)
	if err != nil {
		return fmt.Errorf("write date and time display profile: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read date and time display profile update result: %w", err)
	}
	if count != 1 {
		return errors.New("date and time display profile row is missing")
	}
	return nil
}
