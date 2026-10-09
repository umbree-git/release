package store

import "time"

func (s *Store) Current(component, channel string) (*ReleaseVersion, error) { return nil, ErrNotFound }

func (s *Store) Promotable(component, channel string) ([]ReleaseVersion, error) { return nil, nil }

func (s *Store) IsPromotable(rv ReleaseVersion) (bool, error) { return false, nil }

func (s *Store) Promote(id int64, at time.Time) error { return nil }
