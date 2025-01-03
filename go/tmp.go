chairs := []ChairWithLatLon{}
err = tx.Select(
	&chairs,
	`
-- 近くに存在している椅子一覧
WITH near_chairs AS (
SELECT cl.*
FROM (
	SELECT cl.*, row_number() over (partition BY chair_id ORDER BY created_at DESC) AS rn
	FROM chair_locations cl
) cl
WHERE cl.rn = 1 AND abs(cl.latitude - ?) + abs(cl.longitude - ?) < ?
),

-- すべての椅子の最新のステータス
chair_latest_status AS (
SELECT *
FROM (
	SELECT rides.*, ride_statuses.status AS ride_status, row_number() over (partition BY chair_id ORDER BY ride_statuses.created_at DESC) AS rn
	FROM rides LEFT JOIN ride_statuses ON rides.id = ride_statuses.ride_id		
) r 
WHERE r.rn = 1 AND r.ride_status = 'COMPLETED'
)

SELECT
chairs.*, near_chairs.latitude, near_chairs.longitude
FROM 
chairs
-- ここのINNER JOINで近くに存在している椅子に絞り込まれる
INNER JOIN near_chairs ON chairs.id = near_chairs.chair_id
LEFT JOIN chair_latest_status ON chairs.id = chair_latest_status.chair_id
WHERE
-- 最新のライドが完了しているか1度もライドに割り当てられていなくて現在アクティブな椅子を取得する
(chair_latest_status.ride_status = 'COMPLETED' OR chair_latest_status.ride_status IS NULL) AND chairs.is_active`,
	lat, lon, distance,
)
if err != nil {
	writeError(w, http.StatusInternalServerError, err)
	return
}

nearbyChairs := make([]appGetNearbyChairsResponseChair, 0, len(chairs))
for _, chair := range chairs {
	nearbyChairs = append(nearbyChairs, appGetNearbyChairsResponseChair{
		ID:    chair.ID,
		Name:  chair.Name,
		Model: chair.Model,
		CurrentCoordinate: Coordinate{
			Latitude:  chair.Latitude,
			Longitude: chair.Longitude,
		},
	})
}