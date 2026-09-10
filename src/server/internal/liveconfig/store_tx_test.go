package liveconfig

import (
	"context"
	"testing"

	"github.com/pashagolub/pgxmock/v4"
)

func TestSetKeysLocksRevisionBeforeReadingOrValidating(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("failed to create mock pool: %v", err)
	}
	defer mock.Close()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT revision FROM game_config_revision WHERE id = 1 FOR UPDATE`).
		WillReturnRows(pgxmock.NewRows([]string{"revision"}).AddRow(int64(7)))
	mock.ExpectQuery(`SELECT tick_rate, sync_interval_sec, units_per_meter`).
		WillReturnRows(pgxmock.NewRows([]string{
			"tick_rate", "sync_interval_sec", "units_per_meter",
			"world_width", "world_height",
			"spawn_min_x", "spawn_max_x", "spawn_min_y", "spawn_max_y",
			"player_base_scale", "debug_mode", "world_background_color",
		}).AddRow(60, 5, 32.0, 6000, 3000, 1500, 3000, 500, 1500, 1.0, false, "#000000"))
	mock.ExpectQuery(`SELECT key, value FROM game_config`).
		WillReturnRows(pgxmock.NewRows([]string{"key", "value"}).
			AddRow("fanout_min_recipients_per_tick", "256").
			AddRow("fanout_max_recipients_per_tick", "1000"))
	mock.ExpectExec(`INSERT INTO game_config`).
		WithArgs("fanout_min_recipients_per_tick", "800").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery(`UPDATE game_config_revision SET revision = revision \+ 1 WHERE id = 1 RETURNING revision`).
		WillReturnRows(pgxmock.NewRows([]string{"revision"}).AddRow(int64(8)))
	mock.ExpectCommit()

	s := &Store{pool: mock}
	revision, err := s.setKeysTx(context.Background(), map[string]string{"fanout_min_recipients_per_tick": "800"})
	if err != nil {
		t.Fatalf("setKeysTx failed: %v", err)
	}
	if revision != 8 {
		t.Fatalf("expected revision 8, got %d", revision)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("queries did not run in the required lock-then-read-then-write order: %v", err)
	}
}

func TestSetKeysRollsBackWithoutWritingWhenMergedConfigInvalid(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("failed to create mock pool: %v", err)
	}
	defer mock.Close()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT revision FROM game_config_revision WHERE id = 1 FOR UPDATE`).
		WillReturnRows(pgxmock.NewRows([]string{"revision"}).AddRow(int64(1)))
	mock.ExpectQuery(`SELECT tick_rate, sync_interval_sec, units_per_meter`).
		WillReturnRows(pgxmock.NewRows([]string{
			"tick_rate", "sync_interval_sec", "units_per_meter",
			"world_width", "world_height",
			"spawn_min_x", "spawn_max_x", "spawn_min_y", "spawn_max_y",
			"player_base_scale", "debug_mode", "world_background_color",
		}).AddRow(60, 5, 32.0, 6000, 3000, 1500, 3000, 500, 1500, 1.0, false, "#000000"))
	mock.ExpectQuery(`SELECT key, value FROM game_config`).
		WillReturnRows(pgxmock.NewRows([]string{"key", "value"}).
			AddRow("max_connections", "999999999"))
	mock.ExpectRollback()

	s := &Store{pool: mock}
	if _, err := s.setKeysTx(context.Background(), map[string]string{"keyframe_divisor": "50"}); err == nil {
		t.Fatalf("expected an untouched invalid stored row to fail the merged validation")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expected rollback without any write when validation fails: %v", err)
	}
}
