package main

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

var (
	chairByAccessToken   = sync.Map{}
	db                   *sqlx.DB
	paymentGatewayURL    string
	chairTotalDistanceCh = make(chan ChairTotalDistance, 1000)
	chairModelByModel    = map[string]ChairModel{}
)

type wrappedDriver struct {
	driver.Driver
}

func (d *wrappedDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return &wrappedConn{Conn: conn}, nil
}

type wrappedConn struct {
	driver.Conn
}

func (c *wrappedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	queryWithCallerInfo := c.addCallerInfo(query)
	if queryerCtx, ok := c.Conn.(driver.QueryerContext); ok {
		return queryerCtx.QueryContext(ctx, queryWithCallerInfo, args)
	}
	return nil, driver.ErrSkip
}

var files []string = []string{"app_handlers.go", "chair_handlers.go", "internal_handlers.go", "owner_handlers.go", "payment_gateway.go"}

func (c *wrappedConn) addCallerInfo(query string) string {
	var (
		file     string
		line     int
		funcName string
	)

	for skip := 0; ; skip++ {
		pc, f, l, ok := runtime.Caller(skip)
		if !ok {
			break
		}

		funcName = runtime.FuncForPC(pc).Name()

		file = shortFileName(f)
		if slices.Contains(files, file) {
			line = l
			break
		}
	}

	if file != "" && funcName != "" {
		comment := fmt.Sprintf("/* %s:%d %s */ ", file, line, funcName)
		return comment + query
	}

	return query
}

func shortFileName(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) > 1 {
		return parts[len(parts)-1]
	}
	return path
}

func chairTotalDistanceProcess(ctx context.Context) {
	chairTotalDistances := []ChairTotalDistance{}

	query := `
		INSERT INTO chair_total_distances (chair_id, total_distance, total_distance_updated_at)
		 VALUES (:chair_id, :total_distance, :total_distance_updated_at)
		 ON DUPLICATE KEY UPDATE total_distance = total_distance + :total_distance, total_distance_updated_at = :total_distance_updated_at
	`

	for {
		select {
		case chairTotalDistance := <-chairTotalDistanceCh:
			chairTotalDistances = append(chairTotalDistances, chairTotalDistance)
		case <-time.After(2 * time.Second):
			if len(chairTotalDistances) == 0 {
				continue
			}

			if _, err := db.NamedExecContext(ctx, query, chairTotalDistances); err != nil {
				slog.Error("failed to update chair_total_distances", err)
			}

			chairTotalDistances = []ChairTotalDistance{}
		case <-ctx.Done():
			return
		}
	}
}

func init() {
	sql.Register("wrapped-mysql", &wrappedDriver{Driver: mysql.MySQLDriver{}})
}

func main() {
	go func() {
		log.Fatal(http.ListenAndServe(":6060", nil))
	}()

	mux := setup()
	slog.Info("Listening on :8080")
	http.ListenAndServe(":8080", mux)
}

func setup() http.Handler {
	host := os.Getenv("ISUCON_DB_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("ISUCON_DB_PORT")
	if port == "" {
		port = "3306"
	}
	_, err := strconv.Atoi(port)
	if err != nil {
		panic(fmt.Sprintf("failed to convert DB port number from ISUCON_DB_PORT environment variable into int: %v", err))
	}
	user := os.Getenv("ISUCON_DB_USER")
	if user == "" {
		user = "isucon"
	}
	password := os.Getenv("ISUCON_DB_PASSWORD")
	if password == "" {
		password = "isucon"
	}
	dbname := os.Getenv("ISUCON_DB_NAME")
	if dbname == "" {
		dbname = "isuride"
	}

	dbConfig := mysql.NewConfig()
	dbConfig.User = user
	dbConfig.Passwd = password
	dbConfig.Addr = net.JoinHostPort(host, port)
	dbConfig.Net = "tcp"
	dbConfig.DBName = dbname
	dbConfig.ParseTime = true
	dbConfig.InterpolateParams = true

	// NOTE: 再起動試験対策
	for {
		_db, err := sqlx.Connect("wrapped-mysql", dbConfig.FormatDSN())

		if err == nil {
			db = _db
			break
		}

		time.Sleep(1 * time.Second)
	}

	mux := chi.NewRouter()
	// mux.Use(middleware.Logger)
	// mux.Use(middleware.Recoverer)
	mux.HandleFunc("POST /api/initialize", postInitialize)

	// app handlers
	{
		mux.HandleFunc("POST /api/app/users", appPostUsers)

		authedMux := mux.With(appAuthMiddleware)
		authedMux.HandleFunc("POST /api/app/payment-methods", appPostPaymentMethods)
		authedMux.HandleFunc("GET /api/app/rides", appGetRides)
		authedMux.HandleFunc("POST /api/app/rides", appPostRides)
		authedMux.HandleFunc("POST /api/app/rides/estimated-fare", appPostRidesEstimatedFare)
		authedMux.HandleFunc("POST /api/app/rides/{ride_id}/evaluation", appPostRideEvaluatation)
		authedMux.HandleFunc("GET /api/app/notification", appGetNotificationWithSSE)
		authedMux.HandleFunc("GET /api/app/nearby-chairs", appGetNearbyChairs)
	}

	// owner handlers
	{
		mux.HandleFunc("POST /api/owner/owners", ownerPostOwners)

		authedMux := mux.With(ownerAuthMiddleware)
		authedMux.HandleFunc("GET /api/owner/sales", ownerGetSales)
		authedMux.HandleFunc("GET /api/owner/chairs", ownerGetChairs)
	}

	// chair handlers
	{
		mux.HandleFunc("POST /api/chair/chairs", chairPostChairs)

		authedMux := mux.With(chairAuthMiddleware)
		authedMux.HandleFunc("POST /api/chair/activity", chairPostActivity)
		authedMux.HandleFunc("POST /api/chair/coordinate", chairPostCoordinate)
		authedMux.HandleFunc("GET /api/chair/notification", chairGetNotification)
		authedMux.HandleFunc("POST /api/chair/rides/{ride_id}/status", chairPostRideStatus)
	}

	// internal handlers
	{
		mux.HandleFunc("GET /api/internal/matching", internalGetMatching)
	}

	return mux
}

type postInitializeRequest struct {
	PaymentServer string `json:"payment_server"`
}

type postInitializeResponse struct {
	Language string `json:"language"`
}

func postInitialize(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	req := &postInitializeRequest{}
	if err := bindJSON(r, req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if out, err := exec.Command("../sql/init.sh").CombinedOutput(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to initialize: %s: %w", string(out), err))
		return
	}

	if _, err := db.ExecContext(ctx, "UPDATE settings SET value = ? WHERE name = 'payment_gateway_url'", req.PaymentServer); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	paymentGatewayURL = req.PaymentServer

	chairTotalDistances := []ChairTotalDistance{}
	query := `SELECT chair_id,
                          SUM(IFNULL(distance, 0)) AS total_distance,
                          MAX(created_at)          AS total_distance_updated_at
                   FROM (SELECT chair_id,
                                created_at,
                                ABS(latitude - LAG(latitude) OVER (PARTITION BY chair_id ORDER BY created_at)) +
                                ABS(longitude - LAG(longitude) OVER (PARTITION BY chair_id ORDER BY created_at)) AS distance
                         FROM chair_locations) tmp
                   GROUP BY chair_id`
	if err := db.SelectContext(ctx, &chairTotalDistances, query); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if _, err := db.NamedExecContext(ctx,
		"INSERT INTO chair_total_distances (chair_id, total_distance, total_distance_updated_at) VALUES (:chair_id, :total_distance, :total_distance_updated_at)",
		chairTotalDistances); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	chairs := []Chair{}
	if err := db.SelectContext(ctx, &chairs, "SELECT * FROM chairs"); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	for _, chair := range chairs {
		chairByAccessToken.Store(chair.AccessToken, chair)
	}

	chairModel := []ChairModel{}
	if err := db.SelectContext(ctx, &chairModel, "SELECT * FROM chair_models"); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	for _, model := range chairModel {
		chairModelByModel[model.Name] = model
	}

	go chairTotalDistanceProcess(ctx)

	writeJSON(w, http.StatusOK, postInitializeResponse{Language: "go"})
}

type Coordinate struct {
	Latitude  int `json:"latitude"`
	Longitude int `json:"longitude"`
}

func bindJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func writeJSON(w http.ResponseWriter, statusCode int, v interface{}) {
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	buf, err := json.Marshal(v)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(statusCode)
	w.Write(buf)
}

func writeError(w http.ResponseWriter, statusCode int, err error) {
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	w.WriteHeader(statusCode)
	buf, marshalError := json.Marshal(map[string]string{"message": err.Error()})
	if marshalError != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"marshaling error failed"}`))
		return
	}
	w.Write(buf)

	slog.Error("error response wrote", err)
}

func secureRandomStr(b int) string {
	k := make([]byte, b)
	if _, err := crand.Read(k); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", k)
}
