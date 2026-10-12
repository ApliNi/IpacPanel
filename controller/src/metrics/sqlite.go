package metrics

import (
	"IpacPanel/controller/src/msg"
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	sqlite1mBucketSeconds        = int64(60)
	defaultSQLiteMaxDay          = 7
	defaultSQLiteCompactAfterDay = 2
	dashboardDeviceKindNIC       = int64(1)
	dashboardDeviceKindDisk      = int64(2)
	sqliteBusyTimeoutMS          = 5000
	sqliteConnectTimeout         = 5 * time.Second
	sqliteCompactBatchTimeout    = 60 * time.Second
	sqliteCompactBatchDuration   = 2 * time.Hour
	dashboardSQLiteDBName        = "dashboard.sqlite3"
)

type sqliteDeviceKey struct {
	kind int64
	name string
}

type sqliteDeviceAggregate struct {
	count    int64
	readSum  uint64
	writeSum uint64
	readMax  uint64
	writeMax uint64
}

type sqliteBucketAggregate struct {
	bucketTS         int64
	count            int64
	cpuSum           float64
	cpuMax           float64
	memoryUsedSum    uint64
	memoryUsedMax    uint64
	memoryTotalBytes uint64
	swapUsedSum      uint64
	swapUsedMax      uint64
	swapTotalBytes   uint64
	networkRxSum     uint64
	networkTxSum     uint64
	networkRxMax     uint64
	networkTxMax     uint64
	diskReadSum      uint64
	diskWriteSum     uint64
	diskReadMax      uint64
	diskWriteMax     uint64
	tcpConnectionSum uint64
	udpConnectionSum uint64
	tcpConnectionMax uint64
	udpConnectionMax uint64
	devices          map[sqliteDeviceKey]*sqliteDeviceAggregate
}

func (c *Collector) takeSQLitePendingLocked() []Sample {
	if len(c.sqlitePending) == 0 {
		return nil
	}
	pending := append([]Sample(nil), c.sqlitePending...)
	c.sqlitePending = nil
	return pending
}

func (c *Collector) flushSQLitePending() {
	c.mu.Lock()
	if c.storageMode != storageModeSQLite {
		c.mu.Unlock()
		return
	}
	pending := c.takeSQLitePendingLocked()
	path := c.sqlitePath
	generation := c.sqliteGeneration
	c.mu.Unlock()
	c.writeSQLiteSamplesToCurrentPath(path, generation, pending)
}

func (c *Collector) flushSQLitePendingToPath(path string) {
	c.mu.Lock()
	pending := c.takeSQLitePendingLocked()
	c.mu.Unlock()
	c.writeSQLiteSamplesToStandalonePath(path, pending)
}

func (c *Collector) writeSQLiteSamplesToCurrentPath(path string, generation uint64, samples []Sample) {
	if len(samples) == 0 {
		return
	}
	if err := c.ensureSQLite(path, generation); err != nil {
		log.Printf(msg.DashboardSQLiteInitFailedWarningLogFmt, err)
		return
	}
	c.sqliteMu.Lock()
	db := c.currentDBForPath(path, generation)
	if db == nil {
		c.sqliteMu.Unlock()
		c.writeSQLiteSamplesToStandalonePath(path, samples)
		return
	}
	err := writeSQLiteSamples(db, samples)
	c.sqliteMu.Unlock()
	if err != nil {
		log.Printf(msg.DashboardSQLiteWriteDataFailedWarningLogFmt, err)
	}
}

func (c *Collector) writeSQLiteSamplesToStandalonePath(path string, samples []Sample) {
	if len(samples) == 0 {
		return
	}
	path = normalizeSQLitePath(path)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		log.Printf(msg.DashboardSQLiteCreateDirFailedWarningLogFmt, err)
		return
	}
	db, err := openSQLite(path)
	if err != nil {
		log.Printf(msg.DashboardSQLiteInitFailedWarningLogFmt, err)
		return
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf(msg.DashboardSQLiteCloseFailedWarningLogFmt, err)
		}
	}()
	if err := writeSQLiteSamples(db, samples); err != nil {
		log.Printf(msg.DashboardSQLiteWriteDataFailedWarningLogFmt, err)
	}
}

func (c *Collector) startSQLiteMaintenance(path string, maxDay int, compactAfterDay int, generation uint64) {
	go func() {
		if err := c.ensureSQLite(path, generation); err != nil {
			log.Printf(msg.DashboardSQLiteInitFailedWarningLogFmt, err)
			return
		}
		if !c.sqliteStateMatches(path, generation) {
			return
		}
		c.cleanupSQLiteIfCurrent(path, maxDay, compactAfterDay, generation)
	}()
}

func (c *Collector) sqliteStateMatches(path string, generation uint64) bool {
	path = normalizeSQLitePath(path)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled || c.storageMode != storageModeSQLite || c.sqlitePath != path {
		return false
	}
	return generation == 0 || c.sqliteGeneration == generation
}

func (c *Collector) ensureSQLite(path string, generation uint64) error {
	path = normalizeSQLitePath(path)
	if generation != 0 && !c.sqliteStateMatches(path, generation) {
		return nil
	}
	c.sqliteMu.Lock()
	defer c.sqliteMu.Unlock()
	if generation != 0 && !c.sqliteStateMatches(path, generation) {
		return nil
	}
	c.mu.Lock()
	if c.db != nil && c.dbPath == path {
		c.mu.Unlock()
		return nil
	}
	oldDB := c.db
	c.db = nil
	c.dbPath = ""
	c.mu.Unlock()
	if oldDB != nil {
		if err := oldDB.Close(); err != nil {
			log.Printf(msg.DashboardSQLiteCloseOldFailedWarningLogFmt, err)
		}
	}
	if generation != 0 && !c.sqliteStateMatches(path, generation) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf(msg.DashboardSQLiteCreateDirFailedFmt, err)
	}
	db, err := openSQLite(path)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if generation != 0 && (!c.enabled || c.storageMode != storageModeSQLite || c.sqlitePath != path || c.sqliteGeneration != generation) {
		c.mu.Unlock()
		if err := db.Close(); err != nil {
			log.Printf(msg.DashboardSQLiteCloseExpiredFailedWarningLogFmt, err)
		}
		return nil
	}
	c.db = db
	c.dbPath = path
	c.mu.Unlock()
	return nil
}

func (c *Collector) closeSQLite() {
	c.sqliteMu.Lock()
	defer c.sqliteMu.Unlock()
	c.mu.Lock()
	db := c.db
	c.db = nil
	c.dbPath = ""
	c.mu.Unlock()
	if db != nil {
		if err := db.Close(); err != nil {
			log.Printf(msg.DashboardSQLiteCloseFailedWarningLogFmt, err)
		}
	}
}

func (c *Collector) currentDBForPath(path string, generation uint64) *sql.DB {
	path = normalizeSQLitePath(path)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled || c.storageMode != storageModeSQLite || c.sqlitePath != path || c.dbPath != path {
		return nil
	}
	if generation != 0 && c.sqliteGeneration != generation {
		return nil
	}
	return c.db
}

func openSQLite(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)", filepath.ToSlash(path), sqliteBusyTimeoutMS)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf(msg.DashboardSQLiteOpenFailedFmt, err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	ctx, cancel := context.WithTimeout(context.Background(), sqliteConnectTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf(msg.DashboardSQLiteConnectFailedFmt, err)
	}
	if err := initSQLiteSchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func initSQLiteSchema(ctx context.Context, db *sql.DB) error {
	statements := []string{
		`PRAGMA auto_vacuum = INCREMENTAL`,
		`CREATE TABLE IF NOT EXISTS dashboard_devices (
	id INTEGER PRIMARY KEY,
	kind INTEGER NOT NULL,
	name TEXT NOT NULL,
	first_seen_ts INTEGER NOT NULL,
	last_seen_ts INTEGER NOT NULL,
	UNIQUE(kind, name)
)`,
		`CREATE TABLE IF NOT EXISTS dashboard_samples_1s (
	ts INTEGER NOT NULL,
	seq INTEGER NOT NULL,
	cpu_percent REAL NOT NULL,
	memory_used_bytes INTEGER NOT NULL,
	memory_total_bytes INTEGER NOT NULL,
	swap_used_bytes INTEGER NOT NULL,
	swap_total_bytes INTEGER NOT NULL,
	network_rx_bps INTEGER NOT NULL,
	network_tx_bps INTEGER NOT NULL,
	disk_read_bps INTEGER NOT NULL,
	disk_write_bps INTEGER NOT NULL,
	tcp_connection_count INTEGER NOT NULL,
	udp_connection_count INTEGER NOT NULL,
	PRIMARY KEY (ts, seq)
) WITHOUT ROWID`,
		`CREATE TABLE IF NOT EXISTS dashboard_device_samples_1s (
	ts INTEGER NOT NULL,
	seq INTEGER NOT NULL,
	device_id INTEGER NOT NULL,
	read_bps INTEGER NOT NULL,
	write_bps INTEGER NOT NULL,
	PRIMARY KEY (device_id, ts, seq),
	FOREIGN KEY (device_id) REFERENCES dashboard_devices(id) ON DELETE CASCADE
) WITHOUT ROWID`,
		`CREATE INDEX IF NOT EXISTS idx_dashboard_device_samples_1s_ts_device ON dashboard_device_samples_1s(ts, device_id)`,
		`CREATE TABLE IF NOT EXISTS dashboard_samples_1m (
	bucket_ts INTEGER PRIMARY KEY,
	sample_count INTEGER NOT NULL,
	cpu_avg REAL NOT NULL,
	cpu_max REAL NOT NULL,
	memory_used_avg INTEGER NOT NULL,
	memory_used_max INTEGER NOT NULL,
	memory_total_bytes INTEGER NOT NULL,
	swap_used_avg INTEGER NOT NULL,
	swap_used_max INTEGER NOT NULL,
	swap_total_bytes INTEGER NOT NULL,
	network_rx_avg INTEGER NOT NULL,
	network_tx_avg INTEGER NOT NULL,
	network_rx_max INTEGER NOT NULL,
	network_tx_max INTEGER NOT NULL,
	disk_read_avg INTEGER NOT NULL,
	disk_write_avg INTEGER NOT NULL,
	disk_read_max INTEGER NOT NULL,
	disk_write_max INTEGER NOT NULL,
	tcp_connection_avg INTEGER NOT NULL,
	udp_connection_avg INTEGER NOT NULL,
	tcp_connection_max INTEGER NOT NULL,
	udp_connection_max INTEGER NOT NULL
) WITHOUT ROWID`,
		`CREATE TABLE IF NOT EXISTS dashboard_device_samples_1m (
	bucket_ts INTEGER NOT NULL,
	device_id INTEGER NOT NULL,
	sample_count INTEGER NOT NULL,
	read_avg INTEGER NOT NULL,
	write_avg INTEGER NOT NULL,
	read_max INTEGER NOT NULL,
	write_max INTEGER NOT NULL,
	PRIMARY KEY (device_id, bucket_ts),
	FOREIGN KEY (device_id) REFERENCES dashboard_devices(id) ON DELETE CASCADE
) WITHOUT ROWID`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf(msg.DashboardSQLiteInitSchemaFailedFmt, err)
		}
	}
	if err := migrateLegacySQLiteSchema(ctx, db); err != nil {
		return err
	}
	cleanupStatements := []string{
		`DROP INDEX IF EXISTS idx_dashboard_samples_ts`,
		`DROP INDEX IF EXISTS idx_dashboard_device_samples_lookup`,
		`DROP TABLE IF EXISTS dashboard_device_samples_10s`,
		`DROP TABLE IF EXISTS dashboard_samples_10s`,
		`DROP TABLE IF EXISTS dashboard_device_samples`,
		`DROP TABLE IF EXISTS dashboard_samples`,
		`DROP TABLE IF EXISTS dashboard_hardware`,
		`DROP TABLE IF EXISTS dashboard_device_metric_1m`,
		`DROP TABLE IF EXISTS dashboard_metric_1m`,
	}
	for _, statement := range cleanupStatements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf(msg.DashboardSQLiteCleanupLegacySchemaFailedFmt, err)
		}
	}
	return nil
}

func migrateLegacySQLiteSchema(ctx context.Context, db *sql.DB) error {
	legacySamples, err := sqliteTableExists(ctx, db, "dashboard_samples")
	if err != nil {
		return err
	}
	if !legacySamples {
		return nil
	}
	legacyDevices, err := sqliteTableExists(ctx, db, "dashboard_device_samples")
	if err != nil {
		return err
	}
	legacyHardware, err := sqliteTableExists(ctx, db, "dashboard_hardware")
	if err != nil {
		return err
	}
	statements := []string{
		`INSERT OR IGNORE INTO dashboard_samples_1s (
	ts,
	seq,
	cpu_percent,
	memory_used_bytes,
	memory_total_bytes,
	swap_used_bytes,
	swap_total_bytes,
	network_rx_bps,
	network_tx_bps,
	disk_read_bps,
	disk_write_bps,
	tcp_connection_count,
	udp_connection_count
)
SELECT
	ts,
	seq,
	cpu_percent,
	memory_used_bytes,
	memory_total_bytes,
	swap_used_bytes,
	swap_total_bytes,
	network_rx_bps,
	network_tx_bps,
	disk_read_bps,
	disk_write_bps,
	tcp_connection_count,
	udp_connection_count
FROM dashboard_samples`,
	}
	if legacyHardware {
		statements = append(statements, `INSERT OR IGNORE INTO dashboard_devices (
	kind,
	name,
	first_seen_ts,
	last_seen_ts
)
SELECT
	CASE kind WHEN 'nic' THEN 1 WHEN 'disk' THEN 2 ELSE 0 END,
	name,
	first_seen_ts,
	last_seen_ts
FROM dashboard_hardware
WHERE kind IN ('nic', 'disk')`)
	}
	if legacyDevices {
		statements = append(statements, `INSERT OR IGNORE INTO dashboard_devices (
	kind,
	name,
	first_seen_ts,
	last_seen_ts
)
SELECT
	CASE kind WHEN 'nic' THEN 1 WHEN 'disk' THEN 2 ELSE 0 END,
	name,
	MIN(ts),
	MAX(ts)
FROM dashboard_device_samples
WHERE kind IN ('nic', 'disk')
GROUP BY kind, name`)
		statements = append(statements, `INSERT OR IGNORE INTO dashboard_device_samples_1s (
	ts,
	seq,
	device_id,
	read_bps,
	write_bps
)
SELECT
	ds.ts,
	ds.seq,
	d.id,
	ds.read_bps,
	ds.write_bps
FROM dashboard_device_samples ds
JOIN dashboard_devices d ON d.kind = CASE ds.kind WHEN 'nic' THEN 1 WHEN 'disk' THEN 2 ELSE 0 END
	AND d.name = ds.name
WHERE ds.kind IN ('nic', 'disk')`)
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf(msg.DashboardSQLiteMigrateLegacyFailedFmt, err)
		}
	}
	return nil
}

func sqliteTableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count); err != nil {
		return false, fmt.Errorf(msg.DashboardSQLiteCheckTableFailedFmt, err)
	}
	return count > 0, nil
}

func writeSQLiteSamples(db *sql.DB, samples []Sample) error {
	if len(samples) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), sqliteConnectTimeout)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf(msg.DashboardSQLiteBeginTxFailedFmt, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	sampleStmt, err := tx.PrepareContext(ctx, `INSERT INTO dashboard_samples_1s (
	ts,
	seq,
	cpu_percent,
	memory_used_bytes,
	memory_total_bytes,
	swap_used_bytes,
	swap_total_bytes,
	network_rx_bps,
	network_tx_bps,
	disk_read_bps,
	disk_write_bps,
	tcp_connection_count,
	udp_connection_count
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(ts, seq) DO UPDATE SET
	cpu_percent = excluded.cpu_percent,
	memory_used_bytes = excluded.memory_used_bytes,
	memory_total_bytes = excluded.memory_total_bytes,
	swap_used_bytes = excluded.swap_used_bytes,
	swap_total_bytes = excluded.swap_total_bytes,
	network_rx_bps = excluded.network_rx_bps,
	network_tx_bps = excluded.network_tx_bps,
	disk_read_bps = excluded.disk_read_bps,
	disk_write_bps = excluded.disk_write_bps,
	tcp_connection_count = excluded.tcp_connection_count,
	udp_connection_count = excluded.udp_connection_count`)
	if err != nil {
		return fmt.Errorf(msg.DashboardSQLitePrepareSampleWriteFailedFmt, err)
	}
	defer sampleStmt.Close()
	deviceStmt, err := tx.PrepareContext(ctx, `INSERT INTO dashboard_devices (
	kind,
	name,
	first_seen_ts,
	last_seen_ts
)
VALUES (?, ?, ?, ?)
ON CONFLICT(kind, name) DO UPDATE SET
	first_seen_ts = MIN(first_seen_ts, excluded.first_seen_ts),
	last_seen_ts = MAX(last_seen_ts, excluded.last_seen_ts)
RETURNING id`)
	if err != nil {
		return fmt.Errorf(msg.DashboardSQLitePrepareDeviceWriteFailedFmt, err)
	}
	defer deviceStmt.Close()
	deviceMetricStmt, err := tx.PrepareContext(ctx, `INSERT INTO dashboard_device_samples_1s (
	ts,
	seq,
	device_id,
	read_bps,
	write_bps
)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(device_id, ts, seq) DO UPDATE SET
	read_bps = excluded.read_bps,
	write_bps = excluded.write_bps`)
	if err != nil {
		return fmt.Errorf(msg.DashboardSQLitePrepareDeviceSampleWriteFailedFmt, err)
	}
	defer deviceMetricStmt.Close()
	for i := range samples {
		sample := samples[i]
		ts := sample.Time.Unix()
		if _, err := sampleStmt.ExecContext(ctx, ts, sample.Seq, normalizedFloat(sample.CPUPercent), sample.MemoryUsedBytes, sample.MemoryTotalBytes, sample.SwapUsedBytes, sample.SwapTotalBytes, sample.NetworkRxBPS, sample.NetworkTxBPS, sample.DiskReadBPS, sample.DiskWriteBPS, sample.TCPConnectionCount, sample.UDPConnectionCount); err != nil {
			return fmt.Errorf(msg.DashboardSQLiteWriteSampleFailedFmt, err)
		}
		for name, counter := range sample.interfaceCounters {
			var deviceID int64
			if err := deviceStmt.QueryRowContext(ctx, dashboardDeviceKindNIC, name, ts, ts).Scan(&deviceID); err != nil {
				return fmt.Errorf(msg.DashboardSQLiteWriteDeviceFailedFmt, err)
			}
			if _, err := deviceMetricStmt.ExecContext(ctx, ts, sample.Seq, deviceID, counter.rxBytes, counter.txBytes); err != nil {
				return fmt.Errorf(msg.DashboardSQLiteWriteDeviceSampleFailedFmt, err)
			}
		}
		for name, counter := range sample.diskCounters {
			var deviceID int64
			if err := deviceStmt.QueryRowContext(ctx, dashboardDeviceKindDisk, name, ts, ts).Scan(&deviceID); err != nil {
				return fmt.Errorf(msg.DashboardSQLiteWriteDeviceFailedFmt, err)
			}
			if _, err := deviceMetricStmt.ExecContext(ctx, ts, sample.Seq, deviceID, counter.readBytes, counter.writeBytes); err != nil {
				return fmt.Errorf(msg.DashboardSQLiteWriteDeviceSampleFailedFmt, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf(msg.DashboardSQLiteCommitTxFailedFmt, err)
	}
	committed = true
	return nil
}

func aggregateSQLiteSamples(samples []Sample) []sqliteBucketAggregate {
	buckets := make(map[int64]*sqliteBucketAggregate)
	for i := range samples {
		sample := samples[i]
		bucketTS := sqliteBucketStart(sample.Time)
		aggregate, ok := buckets[bucketTS]
		if !ok {
			aggregate = &sqliteBucketAggregate{bucketTS: bucketTS, devices: make(map[sqliteDeviceKey]*sqliteDeviceAggregate)}
			buckets[bucketTS] = aggregate
		}
		aggregate.addSample(sample)
	}
	keys := make([]int64, 0, len(buckets))
	for bucketTS := range buckets {
		keys = append(keys, bucketTS)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	result := make([]sqliteBucketAggregate, 0, len(keys))
	for _, bucketTS := range keys {
		result = append(result, *buckets[bucketTS])
	}
	return result
}

func (a *sqliteBucketAggregate) addSample(sample Sample) {
	a.count += 1
	cpuPercent := normalizedFloat(sample.CPUPercent)
	a.cpuSum += cpuPercent
	a.cpuMax = maxFloat64(a.cpuMax, cpuPercent)
	a.memoryUsedSum += sample.MemoryUsedBytes
	a.memoryUsedMax = maxUint64(a.memoryUsedMax, sample.MemoryUsedBytes)
	a.memoryTotalBytes = sample.MemoryTotalBytes
	a.swapUsedSum += sample.SwapUsedBytes
	a.swapUsedMax = maxUint64(a.swapUsedMax, sample.SwapUsedBytes)
	a.swapTotalBytes = sample.SwapTotalBytes
	a.networkRxSum += sample.NetworkRxBPS
	a.networkTxSum += sample.NetworkTxBPS
	a.networkRxMax = maxUint64(a.networkRxMax, sample.NetworkRxBPS)
	a.networkTxMax = maxUint64(a.networkTxMax, sample.NetworkTxBPS)
	a.diskReadSum += sample.DiskReadBPS
	a.diskWriteSum += sample.DiskWriteBPS
	a.diskReadMax = maxUint64(a.diskReadMax, sample.DiskReadBPS)
	a.diskWriteMax = maxUint64(a.diskWriteMax, sample.DiskWriteBPS)
	a.tcpConnectionSum += sample.TCPConnectionCount
	a.udpConnectionSum += sample.UDPConnectionCount
	a.tcpConnectionMax = maxUint64(a.tcpConnectionMax, sample.TCPConnectionCount)
	a.udpConnectionMax = maxUint64(a.udpConnectionMax, sample.UDPConnectionCount)
	for name, counter := range sample.interfaceCounters {
		a.addDeviceSample(sqliteDeviceKey{kind: dashboardDeviceKindNIC, name: name}, counter.rxBytes, counter.txBytes)
	}
	for name, counter := range sample.diskCounters {
		a.addDeviceSample(sqliteDeviceKey{kind: dashboardDeviceKindDisk, name: name}, counter.readBytes, counter.writeBytes)
	}
}

func (a *sqliteBucketAggregate) addDeviceSample(key sqliteDeviceKey, read uint64, write uint64) {
	deviceAggregate := a.devices[key]
	if deviceAggregate == nil {
		deviceAggregate = &sqliteDeviceAggregate{}
		a.devices[key] = deviceAggregate
	}
	deviceAggregate.count += 1
	deviceAggregate.readSum += read
	deviceAggregate.writeSum += write
	deviceAggregate.readMax = maxUint64(deviceAggregate.readMax, read)
	deviceAggregate.writeMax = maxUint64(deviceAggregate.writeMax, write)
}

func (a sqliteBucketAggregate) cpuAvg() float64 {
	if a.count <= 0 {
		return 0
	}
	return a.cpuSum / float64(a.count)
}

func (a sqliteBucketAggregate) uintAvg(sum uint64) uint64 {
	if a.count <= 0 {
		return 0
	}
	return sum / uint64(a.count)
}

func (a sqliteDeviceAggregate) readAvg() uint64 {
	if a.count <= 0 {
		return 0
	}
	return a.readSum / uint64(a.count)
}

func (a sqliteDeviceAggregate) writeAvg() uint64 {
	if a.count <= 0 {
		return 0
	}
	return a.writeSum / uint64(a.count)
}

func sqliteBucketStart(sampleTime time.Time) int64 {
	unix := sampleTime.Unix()
	return sqlite1mBucketStart(unix)
}

func sqlite1mBucketStart(unix int64) int64 {
	return unix - unix%sqlite1mBucketSeconds
}

func (c *Collector) querySQLiteSamples(path string, generation uint64, from time.Time, to time.Time, maxPoints int, nic string, diskName string) []Sample {
	db := c.currentDBForPath(path, generation)
	if db == nil || to.Before(from) {
		return nil
	}
	fromUnix := from.Unix()
	toUnix := to.Unix()
	from1mBucket := sqlite1mBucketStart(fromUnix)
	to1mBucket := sqlite1mBucketStart(toUnix)
	bucketSeconds := sqliteDashboardQueryBucketSeconds(fromUnix, toUnix, maxPoints)
	samples := querySQLiteBucketedSamples(db, fromUnix, toUnix, from1mBucket, to1mBucket, bucketSeconds, maxPoints)
	if nic != "" {
		c.fillSQLiteBucketedNetworkCounters(db, samples, nic, fromUnix, toUnix, from1mBucket, to1mBucket, bucketSeconds)
	}
	if diskName != "" {
		c.fillSQLiteBucketedDiskCounters(db, samples, diskName, fromUnix, toUnix, from1mBucket, to1mBucket, bucketSeconds)
	}
	return samples
}

func sqliteDashboardQueryBucketSeconds(fromUnix int64, toUnix int64, maxPoints int) int64 {
	if maxPoints <= 0 {
		return 1
	}
	rangeSeconds := toUnix - fromUnix + 1
	if rangeSeconds <= 1 {
		return 1
	}
	bucketSeconds := (rangeSeconds + int64(maxPoints) - 1) / int64(maxPoints)
	if bucketSeconds < 1 {
		return 1
	}
	return bucketSeconds
}

func querySQLiteBucketedSamples(db *sql.DB, fromUnix int64, toUnix int64, from1mBucket int64, to1mBucket int64, bucketSeconds int64, maxPoints int) []Sample {
	if maxPoints <= 0 {
		maxPoints = 1000
	}
	rows, err := db.Query(`WITH source AS (
	SELECT
		ts,
		seq,
		1 AS sample_count,
		cpu_percent AS cpu_avg,
		cpu_percent AS cpu_max,
		memory_used_bytes AS memory_used_avg,
		memory_used_bytes AS memory_used_max,
		memory_total_bytes,
		swap_used_bytes AS swap_used_avg,
		swap_used_bytes AS swap_used_max,
		swap_total_bytes,
		network_rx_bps AS network_rx_avg,
		network_tx_bps AS network_tx_avg,
		network_rx_bps AS network_rx_max,
		network_tx_bps AS network_tx_max,
		disk_read_bps AS disk_read_avg,
		disk_write_bps AS disk_write_avg,
		disk_read_bps AS disk_read_max,
		disk_write_bps AS disk_write_max,
		tcp_connection_count AS tcp_connection_avg,
		udp_connection_count AS udp_connection_avg,
		tcp_connection_count AS tcp_connection_max,
		udp_connection_count AS udp_connection_max
	FROM dashboard_samples_1s
	WHERE ts BETWEEN ? AND ?
		AND NOT EXISTS (
			SELECT 1
			FROM dashboard_samples_1m minute_sample
			WHERE minute_sample.bucket_ts = ts - (ts % 60)
		)

	UNION ALL

	SELECT
		bucket_ts AS ts,
		bucket_ts AS seq,
		sample_count,
		cpu_avg,
		cpu_max,
		memory_used_avg,
		memory_used_max,
		memory_total_bytes,
		swap_used_avg,
		swap_used_max,
		swap_total_bytes,
		network_rx_avg,
		network_tx_avg,
		network_rx_max,
		network_tx_max,
		disk_read_avg,
		disk_write_avg,
		disk_read_max,
		disk_write_max,
		tcp_connection_avg,
		udp_connection_avg,
		tcp_connection_max,
		udp_connection_max
	FROM dashboard_samples_1m
	WHERE bucket_ts BETWEEN ? AND ?
), bucketed AS (
	SELECT
		? + ((ts - ?) / ?) * ? AS bucket_ts,
		MIN(seq) AS seq,
		SUM(sample_count) AS total_count,
		SUM(cpu_avg * sample_count) / SUM(sample_count) AS cpu_percent,
		CAST(SUM(memory_used_avg * sample_count) / SUM(sample_count) AS INTEGER) AS memory_used_bytes,
		MAX(memory_total_bytes) AS memory_total_bytes,
		CAST(SUM(swap_used_avg * sample_count) / SUM(sample_count) AS INTEGER) AS swap_used_bytes,
		MAX(swap_total_bytes) AS swap_total_bytes,
		MAX(network_rx_max) AS network_rx_bps,
		MAX(network_tx_max) AS network_tx_bps,
		MAX(disk_read_max) AS disk_read_bps,
		MAX(disk_write_max) AS disk_write_bps,
		MAX(tcp_connection_max) AS tcp_connection_count,
		MAX(udp_connection_max) AS udp_connection_count
	FROM source
	GROUP BY bucket_ts
)
SELECT
	bucket_ts,
	seq,
	cpu_percent,
	memory_used_bytes,
	memory_total_bytes,
	swap_used_bytes,
	swap_total_bytes,
	network_rx_bps,
	network_tx_bps,
	disk_read_bps,
	disk_write_bps,
	tcp_connection_count,
	udp_connection_count
FROM bucketed
ORDER BY bucket_ts ASC
LIMIT ?`, fromUnix, toUnix, from1mBucket, to1mBucket, fromUnix, fromUnix, bucketSeconds, bucketSeconds, maxPoints)
	if err != nil {
		log.Printf(msg.DashboardSQLiteQueryAggregateSamplesFailedWarningLogFmt, err)
		return nil
	}
	defer rows.Close()
	samples := make([]Sample, 0)
	for rows.Next() {
		var sample Sample
		var ts int64
		if err := rows.Scan(&ts, &sample.Seq, &sample.CPUPercent, &sample.MemoryUsedBytes, &sample.MemoryTotalBytes, &sample.SwapUsedBytes, &sample.SwapTotalBytes, &sample.NetworkRxBPS, &sample.NetworkTxBPS, &sample.DiskReadBPS, &sample.DiskWriteBPS, &sample.TCPConnectionCount, &sample.UDPConnectionCount); err != nil {
			log.Printf(msg.DashboardSQLiteParseAggregateSamplesFailedWarningLogFmt, err)
			return samples
		}
		sample.Time = time.Unix(ts, 0)
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		log.Printf(msg.DashboardSQLiteScanAggregateSamplesFailedWarningLogFmt, err)
	}
	return samples
}

func (c *Collector) querySQLiteHardware(path string, generation uint64) ([]string, []string) {
	db := c.currentDBForPath(path, generation)
	if db == nil {
		return nil, nil
	}
	rows, err := db.Query(`SELECT kind, name FROM dashboard_devices ORDER BY kind ASC, name ASC`)
	if err != nil {
		log.Printf(msg.DashboardSQLiteQueryHardwareFailedWarningLogFmt, err)
		return nil, nil
	}
	defer rows.Close()
	interfaces := make([]string, 0)
	disks := make([]string, 0)
	for rows.Next() {
		var kind int64
		var name string
		if err := rows.Scan(&kind, &name); err != nil {
			log.Printf(msg.DashboardSQLiteParseHardwareFailedWarningLogFmt, err)
			return interfaces, disks
		}
		if kind == dashboardDeviceKindNIC {
			interfaces = append(interfaces, name)
		} else if kind == dashboardDeviceKindDisk {
			disks = append(disks, name)
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf(msg.DashboardSQLiteScanHardwareFailedWarningLogFmt, err)
	}
	return interfaces, disks
}

func (c *Collector) fillSQLiteBucketedNetworkCounters(db *sql.DB, samples []Sample, nic string, fromUnix int64, toUnix int64, from1mBucket int64, to1mBucket int64, bucketSeconds int64) {
	c.fillSQLiteBucketedDeviceCounters(db, samples, dashboardDeviceNIC, nic, fromUnix, toUnix, from1mBucket, to1mBucket, bucketSeconds)
}

func (c *Collector) fillSQLiteBucketedDiskCounters(db *sql.DB, samples []Sample, diskName string, fromUnix int64, toUnix int64, from1mBucket int64, to1mBucket int64, bucketSeconds int64) {
	c.fillSQLiteBucketedDeviceCounters(db, samples, dashboardDeviceDisk, diskName, fromUnix, toUnix, from1mBucket, to1mBucket, bucketSeconds)
}

func (c *Collector) fillSQLiteBucketedDeviceCounters(db *sql.DB, samples []Sample, kind string, name string, fromUnix int64, toUnix int64, from1mBucket int64, to1mBucket int64, bucketSeconds int64) {
	if len(samples) == 0 || strings.TrimSpace(name) == "" {
		return
	}
	deviceKind := dashboardSQLiteDeviceKind(kind)
	if deviceKind == 0 {
		return
	}
	rows, err := db.Query(`WITH source AS (
	SELECT
		m.ts,
		m.read_bps AS read_max,
		m.write_bps AS write_max
	FROM dashboard_device_samples_1s m
	JOIN dashboard_devices d ON d.id = m.device_id
	WHERE d.kind = ?
		AND d.name = ?
		AND m.ts BETWEEN ? AND ?
		AND NOT EXISTS (
			SELECT 1
			FROM dashboard_device_samples_1m minute_sample
			WHERE minute_sample.device_id = m.device_id
				AND minute_sample.bucket_ts = m.ts - (m.ts % 60)
		)

	UNION ALL

	SELECT
		m.bucket_ts AS ts,
		m.read_max,
		m.write_max
	FROM dashboard_device_samples_1m m
	JOIN dashboard_devices d ON d.id = m.device_id
	WHERE d.kind = ?
		AND d.name = ?
		AND m.bucket_ts BETWEEN ? AND ?
), bucketed AS (
	SELECT
		? + ((ts - ?) / ?) * ? AS bucket_ts,
		MAX(read_max) AS read_bps,
		MAX(write_max) AS write_bps
	FROM source
	GROUP BY bucket_ts
)
SELECT
	bucket_ts,
	read_bps,
	write_bps
FROM bucketed
ORDER BY bucket_ts ASC`, deviceKind, name, fromUnix, toUnix, deviceKind, name, from1mBucket, to1mBucket, fromUnix, fromUnix, bucketSeconds, bucketSeconds)
	if err != nil {
		log.Printf(msg.DashboardSQLiteQueryDeviceAggregateSamplesFailedWarningLogFmt, err)
		return
	}
	defer rows.Close()
	indexes := make(map[int64]int, len(samples))
	for i := range samples {
		indexes[samples[i].Time.Unix()] = i
	}
	for rows.Next() {
		var ts int64
		var read, write uint64
		if err := rows.Scan(&ts, &read, &write); err != nil {
			log.Printf(msg.DashboardSQLiteParseDeviceAggregateSamplesFailedWarningLogFmt, err)
			return
		}
		index, ok := indexes[ts]
		if !ok {
			continue
		}
		if kind == dashboardDeviceNIC {
			samples[index].interfaceCounters = map[string]networkCounter{name: {rxBytes: read, txBytes: write}}
		} else {
			samples[index].diskCounters = map[string]diskCounter{name: {readBytes: read, writeBytes: write}}
		}
	}
}

func (c *Collector) cleanupSQLiteIfCurrent(path string, maxDay int, compactAfterDay int, generation uint64) {
	if maxDay <= 0 {
		return
	}
	if err := c.compactSQLite1sSamplesIfCurrent(path, compactAfterDay, generation); err != nil {
		log.Printf(msg.DashboardSQLiteCompactOldSecondSamplesFailedWarningLogFmt, err)
	}
	c.sqliteMu.Lock()
	defer c.sqliteMu.Unlock()
	c.cleanupSQLiteDB(c.currentDBForPath(path, generation), maxDay)
}

func (c *Collector) cleanupSQLiteDB(db *sql.DB, maxDay int) {
	if db == nil {
		return
	}
	cutoff := beginningOfLocalDay(time.Now()).AddDate(0, 0, -maxDay+1).Unix()
	if _, err := db.Exec(`DELETE FROM dashboard_device_samples_1s WHERE ts < ?`, cutoff); err != nil {
		log.Printf(msg.DashboardSQLiteCleanupOldSecondDeviceSamplesFailedWarningLogFmt, err)
	}
	if _, err := db.Exec(`DELETE FROM dashboard_samples_1s WHERE ts < ?`, cutoff); err != nil {
		log.Printf(msg.DashboardSQLiteCleanupOldSecondSamplesFailedWarningLogFmt, err)
	}
	if _, err := db.Exec(`DELETE FROM dashboard_device_samples_1m WHERE bucket_ts < ?`, cutoff); err != nil {
		log.Printf(msg.DashboardSQLiteCleanupOldDeviceSamplesFailedWarningLogFmt, err)
	}
	if _, err := db.Exec(`DELETE FROM dashboard_samples_1m WHERE bucket_ts < ?`, cutoff); err != nil {
		log.Printf(msg.DashboardSQLiteCleanupOldSamplesFailedWarningLogFmt, err)
	}
	if _, err := db.Exec(`DELETE FROM dashboard_devices WHERE last_seen_ts < ?`, cutoff); err != nil {
		log.Printf(msg.DashboardSQLiteCleanupOldHardwareFailedWarningLogFmt, err)
	}
	if _, err := db.Exec(`PRAGMA incremental_vacuum`); err != nil {
		log.Printf(msg.DashboardSQLiteVacuumFailedWarningLogFmt, err)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		log.Printf(msg.DashboardSQLiteTruncateWALFailedWarningLogFmt, err)
	}
}

func (c *Collector) compactSQLite1sSamplesIfCurrent(path string, compactAfterDay int, generation uint64) error {
	if compactAfterDay <= 0 {
		return nil
	}
	cutoff := beginningOfLocalDay(time.Now()).AddDate(0, 0, -compactAfterDay).Unix()
	for {
		c.sqliteMu.Lock()
		db := c.currentDBForPath(path, generation)
		if db == nil {
			c.sqliteMu.Unlock()
			return nil
		}
		batchStart, ok, err := querySQLiteCompactBatchStart(db, cutoff)
		if err != nil {
			c.sqliteMu.Unlock()
			return err
		}
		if !ok {
			c.sqliteMu.Unlock()
			return nil
		}
		batchStart = sqlite1mBucketStart(batchStart)
		batchEnd := batchStart + int64(sqliteCompactBatchDuration/time.Second)
		if batchEnd > cutoff {
			batchEnd = cutoff
		}
		if batchEnd <= batchStart {
			c.sqliteMu.Unlock()
			return nil
		}
		err = compactSQLite1sSamplesBatch(db, batchStart, batchEnd)
		c.sqliteMu.Unlock()
		if err != nil {
			return err
		}
		// 批次间让出时间, 避免长时间阻塞 sqliteMu
		time.Sleep(10 * time.Millisecond)
	}
}

func querySQLiteCompactBatchStart(db *sql.DB, cutoff int64) (int64, bool, error) {
	var batchStart sql.NullInt64
	if err := db.QueryRow(`
SELECT MIN(ts)
FROM (
	SELECT MIN(ts) AS ts FROM dashboard_samples_1s WHERE ts < ?
	UNION ALL
	SELECT MIN(ts) AS ts FROM dashboard_device_samples_1s WHERE ts < ?
)`, cutoff, cutoff).Scan(&batchStart); err != nil {
		return 0, false, fmt.Errorf(msg.DashboardSQLiteExecCompactFailedFmt, err)
	}
	if !batchStart.Valid {
		return 0, false, nil
	}
	return batchStart.Int64, true, nil
}

func compactSQLite1sSamplesBatch(db *sql.DB, batchStart int64, batchEnd int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), sqliteCompactBatchTimeout)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf(msg.DashboardSQLiteBeginCompactTxFailedFmt, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	statements := []string{
		`INSERT INTO dashboard_samples_1m (
	bucket_ts,
	sample_count,
	cpu_avg,
	cpu_max,
	memory_used_avg,
	memory_used_max,
	memory_total_bytes,
	swap_used_avg,
	swap_used_max,
	swap_total_bytes,
	network_rx_avg,
	network_tx_avg,
	network_rx_max,
	network_tx_max,
	disk_read_avg,
	disk_write_avg,
	disk_read_max,
	disk_write_max,
	tcp_connection_avg,
	udp_connection_avg,
	tcp_connection_max,
	udp_connection_max
)
SELECT
	ts - (ts % 60) AS bucket_ts,
	COUNT(*) AS sample_count,
	AVG(cpu_percent),
	MAX(cpu_percent),
	CAST(AVG(memory_used_bytes) AS INTEGER),
	MAX(memory_used_bytes),
	MAX(memory_total_bytes),
	CAST(AVG(swap_used_bytes) AS INTEGER),
	MAX(swap_used_bytes),
	MAX(swap_total_bytes),
	CAST(AVG(network_rx_bps) AS INTEGER),
	CAST(AVG(network_tx_bps) AS INTEGER),
	MAX(network_rx_bps),
	MAX(network_tx_bps),
	CAST(AVG(disk_read_bps) AS INTEGER),
	CAST(AVG(disk_write_bps) AS INTEGER),
	MAX(disk_read_bps),
	MAX(disk_write_bps),
	CAST(AVG(tcp_connection_count) AS INTEGER),
	CAST(AVG(udp_connection_count) AS INTEGER),
	MAX(tcp_connection_count),
	MAX(udp_connection_count)
FROM dashboard_samples_1s
WHERE ts >= ? AND ts < ?
GROUP BY bucket_ts
ON CONFLICT(bucket_ts) DO UPDATE SET
	sample_count = excluded.sample_count,
	cpu_avg = excluded.cpu_avg,
	cpu_max = excluded.cpu_max,
	memory_used_avg = excluded.memory_used_avg,
	memory_used_max = excluded.memory_used_max,
	memory_total_bytes = excluded.memory_total_bytes,
	swap_used_avg = excluded.swap_used_avg,
	swap_used_max = excluded.swap_used_max,
	swap_total_bytes = excluded.swap_total_bytes,
	network_rx_avg = excluded.network_rx_avg,
	network_tx_avg = excluded.network_tx_avg,
	network_rx_max = excluded.network_rx_max,
	network_tx_max = excluded.network_tx_max,
	disk_read_avg = excluded.disk_read_avg,
	disk_write_avg = excluded.disk_write_avg,
	disk_read_max = excluded.disk_read_max,
	disk_write_max = excluded.disk_write_max,
	tcp_connection_avg = excluded.tcp_connection_avg,
	udp_connection_avg = excluded.udp_connection_avg,
	tcp_connection_max = excluded.tcp_connection_max,
	udp_connection_max = excluded.udp_connection_max`,
		`INSERT INTO dashboard_device_samples_1m (
	bucket_ts,
	device_id,
	sample_count,
	read_avg,
	write_avg,
	read_max,
	write_max
)
SELECT
	ts - (ts % 60) AS bucket_ts,
	device_id,
	COUNT(*) AS sample_count,
	CAST(AVG(read_bps) AS INTEGER),
	CAST(AVG(write_bps) AS INTEGER),
	MAX(read_bps),
	MAX(write_bps)
FROM dashboard_device_samples_1s
WHERE ts >= ? AND ts < ?
GROUP BY bucket_ts, device_id
ON CONFLICT(device_id, bucket_ts) DO UPDATE SET
	sample_count = excluded.sample_count,
	read_avg = excluded.read_avg,
	write_avg = excluded.write_avg,
	read_max = excluded.read_max,
	write_max = excluded.write_max`,
		`DELETE FROM dashboard_device_samples_1s WHERE ts >= ? AND ts < ?`,
		`DELETE FROM dashboard_samples_1s WHERE ts >= ? AND ts < ?`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement, batchStart, batchEnd); err != nil {
			return fmt.Errorf(msg.DashboardSQLiteExecCompactFailedFmt, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf(msg.DashboardSQLiteCommitCompactTxFailedFmt, err)
	}
	committed = true
	return nil
}

func (c *Collector) cleanupCurrentSQLite() {
	c.mu.Lock()
	storageMode := c.storageMode
	path := c.sqlitePath
	generation := c.sqliteGeneration
	maxDay := c.sqliteMaxDay
	compactAfterDay := c.sqliteCompactAfterDay
	c.mu.Unlock()
	if storageMode != storageModeSQLite {
		return
	}
	c.cleanupSQLiteIfCurrent(path, maxDay, compactAfterDay, generation)
}

func dashboardSQLiteDeviceKind(kind string) int64 {
	switch kind {
	case dashboardDeviceNIC:
		return dashboardDeviceKindNIC
	case dashboardDeviceDisk:
		return dashboardDeviceKindDisk
	default:
		return 0
	}
}

func normalizeSQLiteMaxDay(maxDay int) int {
	if maxDay < 0 {
		return defaultSQLiteMaxDay
	}
	return maxDay
}

func normalizeSQLiteCompactAfterDay(day int) int {
	if day < 0 {
		return defaultSQLiteCompactAfterDay
	}
	return day
}

func normalizeSQLitePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return filepath.Join("data", "dashboard", dashboardSQLiteDBName)
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return filepath.Join(path, dashboardSQLiteDBName)
	}
	if strings.HasSuffix(path, string(os.PathSeparator)) || strings.HasSuffix(path, "/") {
		return filepath.Join(path, dashboardSQLiteDBName)
	}
	return path
}
