package main

import (
	"database/sql"
	"errors"
	"net/http"
)

// このAPIをインスタンス内から一定間隔で叩かせることで、椅子とライドをマッチングさせる
func internalGetMatching(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// MEMO: 一旦最も待たせているリクエストに適当な空いている椅子マッチさせる実装とする。おそらくもっといい方法があるはず…
	ride := &Ride{}
	if err := db.GetContext(ctx, ride, `SELECT * FROM rides WHERE chair_id IS NULL ORDER BY created_at LIMIT 1`); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	matched := &Chair{}
	query := `
		WITH latest_rides AS (
			SELECT *
			FROM (
					SELECT *, ROW_NUMBER() OVER (PARTITION BY chair_id order by created_at desc) as rn
					FROM rides
				) tmp
			WHERE rn = 1
		)

		-- ライドごとの最新の状態
		, latest_ride_statuses AS (
			SELECT *
			FROM (
					SELECT *, ROW_NUMBER() OVER (PARTITION BY ride_id order by created_at desc) as rn
					FROM ride_statuses
				) tmp
			WHERE rn = 1
		)

		-- 状態がCOMPETEDの椅子を適当に一つ取得
		SELECT chairs.*
		FROM chairs
				LEFT JOIN latest_rides ON chairs.id = latest_rides.chair_id
				LEFT JOIN latest_ride_statuses ON latest_rides.id = latest_ride_statuses.ride_id
		WHERE (latest_ride_statuses.status = 'COMPLETED' OR latest_ride_statuses.status IS NULL) AND is_active
		LIMIT 1
	`

	if err := db.GetContext(ctx, matched, query); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// empty := false
	// for i := 0; i < 10; i++ {
	// 	if err := db.GetContext(ctx, matched, "SELECT * FROM chairs INNER JOIN (SELECT id FROM chairs WHERE is_active = TRUE ORDER BY RAND() LIMIT 1) AS tmp ON chairs.id = tmp.id LIMIT 1"); err != nil {
	// 		if errors.Is(err, sql.ErrNoRows) {
	// 			w.WriteHeader(http.StatusNoContent)
	// 			return
	// 		}
	// 		writeError(w, http.StatusInternalServerError, err)
	// 	}

	// 	if err := db.GetContext(ctx, &empty, "SELECT COUNT(*) = 0 FROM (SELECT COUNT(chair_sent_at) = 6 AS completed FROM ride_statuses WHERE ride_id IN (SELECT id FROM rides WHERE chair_id = ?) GROUP BY ride_id) is_completed WHERE completed = FALSE", matched.ID); err != nil {
	// 		writeError(w, http.StatusInternalServerError, err)
	// 		return
	// 	}
	// 	if empty {
	// 		break
	// 	}
	// }
	// if !empty {
	// 	w.WriteHeader(http.StatusNoContent)
	// 	return
	// }

	if _, err := db.ExecContext(ctx, "UPDATE rides SET chair_id = ? WHERE id = ?", matched.ID, ride.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
