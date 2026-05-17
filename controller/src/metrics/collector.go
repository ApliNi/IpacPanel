package metrics

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	_ "modernc.org/sqlite"
)

const (
	sampleInterval               = time.Second
	dashboardMemoryRetention     = 30 * time.Second
	sqliteFlushInterval          = 30 * time.Second
	sqliteCleanupInterval        = time.Hour
	sqlite1mBucketSeconds        = int64(60)
	defaultMemoryMaxMin          = 30
	defaultSQLiteMaxDay          = 7
	defaultSQLiteCompactAfterDay = 2
	storageModeMemory            = "memory"
	storageModeSQLite            = "sqlite"
	dashboardDeviceNIC           = "nic"
	dashboardDeviceDisk          = "disk"
	dashboardDeviceKindNIC       = int64(1)
	dashboardDeviceKindDisk      = int64(2)
	sqliteBusyTimeoutMS          = 5000
	sqliteConnectTimeout         = 5 * time.Second
	dashboardSQLiteDBName        = "dashboard.sqlite3"
)

type Config struct {
	Enabled               bool
	StorageMode           string
	MemoryMaxMin          int
	SQLiteMaxDay          int
	SQLiteCompactAfterDay int
	SQLitePath            string
}

type Sample struct {
	Seq                int64     `json:"seq"`
	Time               time.Time `json:"time"`
	CPUPercent         float64   `json:"cpu_percent"`
	MemoryUsedBytes    uint64    `json:"memory_used_bytes"`
	MemoryTotalBytes   uint64    `json:"memory_total_bytes"`
	SwapUsedBytes      uint64    `json:"swap_used_bytes"`
	SwapTotalBytes     uint64    `json:"swap_total_bytes"`
	NetworkRxBPS       uint64    `json:"network_rx_bps"`
	NetworkTxBPS       uint64    `json:"network_tx_bps"`
	TCPConnectionCount uint64    `json:"tcp_connection_count"`
	UDPConnectionCount uint64    `json:"udp_connection_count"`
	DiskReadBPS        uint64    `json:"disk_read_bps"`
	DiskWriteBPS       uint64    `json:"disk_write_bps"`
	interfaceCounters  map[string]networkCounter
	diskCounters       map[string]diskCounter
}

type PublicSample struct {
	Time               time.Time `json:"time"`
	CPUPercent         float64   `json:"cpu_percent"`
	MemoryUsedBytes    uint64    `json:"memory_used_bytes"`
	MemoryTotalBytes   uint64    `json:"memory_total_bytes"`
	SwapUsedBytes      uint64    `json:"swap_used_bytes"`
	SwapTotalBytes     uint64    `json:"swap_total_bytes"`
	NetworkRxBPS       uint64    `json:"network_rx_bps"`
	NetworkTxBPS       uint64    `json:"network_tx_bps"`
	TCPConnectionCount uint64    `json:"tcp_connection_count"`
	UDPConnectionCount uint64    `json:"udp_connection_count"`
	DiskReadBPS        uint64    `json:"disk_read_bps"`
	DiskWriteBPS       uint64    `json:"disk_write_bps"`
}

type Snapshot struct {
	Enabled      bool           `json:"enabled"`
	MemoryMaxMin int            `json:"memory_max_min"`
	Interfaces   []string       `json:"interfaces"`
	Disks        []string       `json:"disks"`
	Samples      []PublicSample `json:"samples"`
	Latest       *PublicSample  `json:"latest"`
}

type Metadata struct {
	Enabled      bool
	MemoryMaxMin int
	Interfaces   []string
	Disks        []string
}

type networkCounter struct {
	rxBytes uint64
	txBytes uint64
}

type diskCounter struct {
	readBytes  uint64
	writeBytes uint64
}

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

type Collector struct {
	mu                    sync.Mutex
	enabled               bool
	retentionMinutes      int
	storageMode           string
	sqliteMaxDay          int
	sqliteCompactAfterDay int
	sqlitePath            string
	nextSeq               int64
	samples               []Sample
	sqlitePending         []Sample
	interfaces            map[string]struct{}
	disks                 map[string]struct{}
	stopCh                chan struct{}
	doneCh                chan struct{}
	lastNetwork           map[string]networkCounter
	lastNetworkTime       time.Time
	lastDisk              map[string]diskCounter
	lastDiskTime          time.Time
	db                    *sql.DB
	dbPath                string
}

func NewCollector(config Config) *Collector {
	c := &Collector{
		retentionMinutes:      normalizeRetentionMinutes(config.MemoryMaxMin),
		storageMode:           normalizeStorageMode(config.StorageMode),
		sqliteMaxDay:          normalizeSQLiteMaxDay(config.SQLiteMaxDay),
		sqliteCompactAfterDay: normalizeSQLiteCompactAfterDay(config.SQLiteCompactAfterDay),
		sqlitePath:            normalizeSQLitePath(config.SQLitePath),
		interfaces:            make(map[string]struct{}),
		disks:                 make(map[string]struct{}),
	}
	c.ApplyConfig(config)
	return c
}

func (c *Collector) ApplyConfig(config Config) {
	retentionMinutes := normalizeRetentionMinutes(config.MemoryMaxMin)
	storageMode := normalizeStorageMode(config.StorageMode)
	sqliteMaxDay := normalizeSQLiteMaxDay(config.SQLiteMaxDay)
	sqliteCompactAfterDay := normalizeSQLiteCompactAfterDay(config.SQLiteCompactAfterDay)
	sqlitePath := normalizeSQLitePath(config.SQLitePath)

	c.mu.Lock()
	previousStorageMode := c.storageMode
	previousSQLitePath := c.sqlitePath
	c.retentionMinutes = retentionMinutes
	c.storageMode = storageMode
	c.sqliteMaxDay = sqliteMaxDay
	c.sqliteCompactAfterDay = sqliteCompactAfterDay
	c.sqlitePath = sqlitePath
	if !config.Enabled || retentionMinutes <= 0 {
		c.enabled = false
		c.samples = nil
		c.interfaces = make(map[string]struct{})
		c.disks = make(map[string]struct{})
		stopCh := c.stopCh
		doneCh := c.doneCh
		c.stopCh = nil
		c.doneCh = nil
		c.lastNetwork = nil
		c.lastNetworkTime = time.Time{}
		c.lastDisk = nil
		c.lastDiskTime = time.Time{}
		c.mu.Unlock()
		c.stopRunLoop(stopCh, doneCh)
		if previousStorageMode == storageModeSQLite {
			c.flushSQLitePendingToPath(previousSQLitePath)
		}
		c.closeSQLite()
		return
	}
	if c.enabled {
		pathChanged := previousSQLitePath != sqlitePath || previousStorageMode != storageMode
		c.trimLocked(time.Now())
		c.mu.Unlock()
		if previousStorageMode == storageModeSQLite && pathChanged {
			c.flushSQLitePendingToPath(previousSQLitePath)
		}
		if storageMode == storageModeSQLite {
			c.ensureSQLite(sqlitePath)
			c.cleanupSQLite(sqliteMaxDay, sqliteCompactAfterDay)
		} else {
			c.closeSQLite()
		}
		return
	}
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	c.enabled = true
	c.stopCh = stopCh
	c.doneCh = doneCh
	c.lastNetwork = nil
	c.lastNetworkTime = time.Time{}
	c.lastDisk = nil
	c.lastDiskTime = time.Time{}
	c.mu.Unlock()
	if storageMode == storageModeSQLite {
		c.ensureSQLite(sqlitePath)
		c.cleanupSQLite(sqliteMaxDay, sqliteCompactAfterDay)
	}
	go c.run(stopCh, doneCh)
}

func (c *Collector) Stop() {
	c.mu.Lock()
	stopCh := c.stopCh
	doneCh := c.doneCh
	storageMode := c.storageMode
	sqlitePath := c.sqlitePath
	c.enabled = false
	c.stopCh = nil
	c.doneCh = nil
	c.samples = nil
	c.interfaces = make(map[string]struct{})
	c.disks = make(map[string]struct{})
	c.lastNetwork = nil
	c.lastNetworkTime = time.Time{}
	c.lastDisk = nil
	c.lastDiskTime = time.Time{}
	c.mu.Unlock()
	c.stopRunLoop(stopCh, doneCh)
	if storageMode == storageModeSQLite {
		c.flushSQLitePendingToPath(sqlitePath)
	}
	c.closeSQLite()
}

func (c *Collector) Snapshot(minutes int, nic string, disk string, maxPoints int) Snapshot {
	selectedMinutes := normalizeSelectedMinutes(minutes)
	now := time.Now()
	cutoff := now.Add(-time.Duration(selectedMinutes) * time.Minute)
	nic = strings.TrimSpace(nic)
	disk = strings.TrimSpace(disk)

	c.mu.Lock()
	enabled := c.enabled
	retentionMinutes := c.retentionMinutes
	storageMode := c.storageMode
	interfaces := sortedInterfacesLocked(c.interfaces)
	disks := sortedDisksLocked(c.disks)
	memory := make([]Sample, 0, len(c.samples))
	for i := range c.samples {
		if c.samples[i].Time.Before(cutoff) {
			continue
		}
		memory = append(memory, c.samples[i])
	}
	c.mu.Unlock()
	if enabled && storageMode == storageModeSQLite {
		storedInterfaces, storedDisks := c.querySQLiteHardware()
		interfaces = mergeSortedStrings(interfaces, storedInterfaces)
		disks = mergeSortedStrings(disks, storedDisks)
	}

	var samples []Sample
	if enabled && storageMode == storageModeSQLite {
		queryTo := now
		if len(memory) > 0 {
			queryTo = memory[0].Time.Add(-time.Second)
		}
		samples = c.querySQLiteSamples(cutoff, queryTo, maxPoints, nic, disk)
		samples = mergeHistorySamples(samples, memory)
		samples = downsampleSamples(samples, maxPoints)
	} else {
		samples = downsampleSamples(memory, maxPoints)
	}

	publicSamples := make([]PublicSample, 0, len(samples))
	for i := range samples {
		publicSamples = append(publicSamples, samples[i].public(nic, disk))
	}
	var latest *PublicSample
	if len(publicSamples) > 0 {
		latestValue := publicSamples[len(publicSamples)-1]
		latest = &latestValue
	}
	return Snapshot{Enabled: enabled, MemoryMaxMin: retentionMinutes, Interfaces: interfaces, Disks: disks, Samples: publicSamples, Latest: latest}
}

func (c *Collector) Latest(nic string, disk string) (PublicSample, bool) {
	nic = strings.TrimSpace(nic)
	disk = strings.TrimSpace(disk)
	c.mu.Lock()
	if !c.enabled || len(c.samples) == 0 {
		c.mu.Unlock()
		return PublicSample{}, false
	}
	latest := c.samples[len(c.samples)-1]
	c.mu.Unlock()
	return latest.public(nic, disk), true
}

func (c *Collector) Metadata() Metadata {
	c.mu.Lock()
	enabled := c.enabled
	storageMode := c.storageMode
	retentionMinutes := c.retentionMinutes
	interfaces := sortedInterfacesLocked(c.interfaces)
	disks := sortedDisksLocked(c.disks)
	c.mu.Unlock()
	if enabled && storageMode == storageModeSQLite {
		storedInterfaces, storedDisks := c.querySQLiteHardware()
		interfaces = mergeSortedStrings(interfaces, storedInterfaces)
		disks = mergeSortedStrings(disks, storedDisks)
	}
	return Metadata{Enabled: enabled, MemoryMaxMin: retentionMinutes, Interfaces: interfaces, Disks: disks}
}

func (c *Collector) run(stopCh <-chan struct{}, doneCh chan<- struct{}) {
	defer close(doneCh)
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	flushTicker := time.NewTicker(sqliteFlushInterval)
	defer flushTicker.Stop()
	cleanupTicker := time.NewTicker(sqliteCleanupInterval)
	defer cleanupTicker.Stop()
	defer c.flushSQLitePending()
	c.collect()
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			c.collect()
		case <-flushTicker.C:
			c.flushSQLitePending()
		case <-cleanupTicker.C:
			c.flushSQLitePending()
			c.cleanupCurrentSQLite()
		}
	}
}

func (c *Collector) collect() {
	now := time.Now()
	cpuPercent := 0.0
	percentValues, err := cpu.Percent(0, false)
	if err == nil && len(percentValues) > 0 {
		cpuPercent = percentValues[0]
	} else if err != nil {
		log.Printf("collect dashboard cpu metrics failed: %v", err)
	}

	virtualMemory, err := mem.VirtualMemory()
	if err != nil {
		log.Printf("collect dashboard memory metrics failed: %v", err)
		virtualMemory = &mem.VirtualMemoryStat{}
	}
	swapMemory, err := mem.SwapMemory()
	if err != nil {
		log.Printf("collect dashboard swap metrics failed: %v", err)
		swapMemory = &mem.SwapMemoryStat{}
	}
	interfaces, networkCounters := readNetworkCounters()
	tcpConnectionCount, udpConnectionCount := readConnectionCounts()
	disks, diskCounters := readDiskCounters()

	c.mu.Lock()
	if !c.enabled {
		c.mu.Unlock()
		return
	}
	networkRxBPS, networkTxBPS, interfaceCounters := c.calculateNetworkBandwidthLocked(now, networkCounters)
	diskReadBPS, diskWriteBPS, selectedDiskCounters := c.calculateDiskBandwidthLocked(now, diskCounters)
	for name := range interfaces {
		c.interfaces[name] = struct{}{}
	}
	for name := range disks {
		c.disks[name] = struct{}{}
	}
	c.nextSeq += 1
	sample := Sample{
		Seq: c.nextSeq, Time: now, CPUPercent: cpuPercent,
		MemoryUsedBytes: virtualMemory.Used, MemoryTotalBytes: virtualMemory.Total,
		SwapUsedBytes: swapMemory.Used, SwapTotalBytes: swapMemory.Total,
		NetworkRxBPS: networkRxBPS, NetworkTxBPS: networkTxBPS,
		TCPConnectionCount: tcpConnectionCount, UDPConnectionCount: udpConnectionCount,
		DiskReadBPS: diskReadBPS, DiskWriteBPS: diskWriteBPS,
		interfaceCounters: interfaceCounters, diskCounters: selectedDiskCounters,
	}
	c.samples = append(c.samples, sample)
	if c.storageMode == storageModeSQLite {
		c.sqlitePending = append(c.sqlitePending, sample)
	}
	c.trimLocked(now)
	c.mu.Unlock()
}

func (c *Collector) stopRunLoop(stopCh chan struct{}, doneCh chan struct{}) {
	if stopCh != nil {
		close(stopCh)
	}
	if doneCh != nil {
		<-doneCh
	}
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
	c.mu.Unlock()
	c.writeSQLiteSamplesToPath(path, pending)
}

func (c *Collector) flushSQLitePendingToPath(path string) {
	c.mu.Lock()
	pending := c.takeSQLitePendingLocked()
	c.mu.Unlock()
	c.writeSQLiteSamplesToPath(path, pending)
}

func (c *Collector) writeSQLiteSamplesToPath(path string, samples []Sample) {
	if len(samples) == 0 {
		return
	}
	if err := c.ensureSQLite(path); err != nil {
		log.Printf("dashboard sqlite warning: 初始化数据库失败: %v", err)
		return
	}
	db := c.currentDB()
	if db == nil {
		log.Printf("dashboard sqlite warning: 数据库未打开")
		return
	}
	if err := writeSQLiteSamples(db, samples); err != nil {
		log.Printf("dashboard sqlite warning: 写入仪表板数据失败: %v", err)
	}
}

func (c *Collector) ensureSQLite(path string) error {
	path = normalizeSQLitePath(path)
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
			log.Printf("dashboard sqlite warning: 关闭旧数据库失败: %v", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("创建仪表板数据库目录失败: %w", err)
	}
	db, err := openSQLite(path)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.db = db
	c.dbPath = path
	c.mu.Unlock()
	return nil
}

func (c *Collector) closeSQLite() {
	c.mu.Lock()
	db := c.db
	c.db = nil
	c.dbPath = ""
	c.mu.Unlock()
	if db != nil {
		if err := db.Close(); err != nil {
			log.Printf("dashboard sqlite warning: 关闭数据库失败: %v", err)
		}
	}
}

func (c *Collector) currentDB() *sql.DB {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.db
}

func openSQLite(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)", filepath.ToSlash(path), sqliteBusyTimeoutMS)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), sqliteConnectTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
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
			return fmt.Errorf("初始化仪表板数据库失败: %w", err)
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
			return fmt.Errorf("清理旧仪表板数据库结构失败: %w", err)
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
			return fmt.Errorf("迁移旧仪表板数据库失败: %w", err)
		}
	}
	return nil
}

func sqliteTableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count); err != nil {
		return false, fmt.Errorf("检查仪表板数据库表失败: %w", err)
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
		return fmt.Errorf("开始事务失败: %w", err)
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
		return fmt.Errorf("准备样本写入失败: %w", err)
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
		return fmt.Errorf("准备设备信息写入失败: %w", err)
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
		return fmt.Errorf("准备设备样本写入失败: %w", err)
	}
	defer deviceMetricStmt.Close()
	for i := range samples {
		sample := samples[i]
		ts := sample.Time.Unix()
		if _, err := sampleStmt.ExecContext(ctx, ts, sample.Seq, normalizedFloat(sample.CPUPercent), sample.MemoryUsedBytes, sample.MemoryTotalBytes, sample.SwapUsedBytes, sample.SwapTotalBytes, sample.NetworkRxBPS, sample.NetworkTxBPS, sample.DiskReadBPS, sample.DiskWriteBPS, sample.TCPConnectionCount, sample.UDPConnectionCount); err != nil {
			return fmt.Errorf("写入样本失败: %w", err)
		}
		for name, counter := range sample.interfaceCounters {
			var deviceID int64
			if err := deviceStmt.QueryRowContext(ctx, dashboardDeviceKindNIC, name, ts, ts).Scan(&deviceID); err != nil {
				return fmt.Errorf("写入设备信息失败: %w", err)
			}
			if _, err := deviceMetricStmt.ExecContext(ctx, ts, sample.Seq, deviceID, counter.rxBytes, counter.txBytes); err != nil {
				return fmt.Errorf("写入设备样本失败: %w", err)
			}
		}
		for name, counter := range sample.diskCounters {
			var deviceID int64
			if err := deviceStmt.QueryRowContext(ctx, dashboardDeviceKindDisk, name, ts, ts).Scan(&deviceID); err != nil {
				return fmt.Errorf("写入设备信息失败: %w", err)
			}
			if _, err := deviceMetricStmt.ExecContext(ctx, ts, sample.Seq, deviceID, counter.readBytes, counter.writeBytes); err != nil {
				return fmt.Errorf("写入设备样本失败: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
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

func (c *Collector) querySQLiteSamples(from time.Time, to time.Time, maxPoints int, nic string, diskName string) []Sample {
	db := c.currentDB()
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
		log.Printf("dashboard sqlite warning: 查询仪表板聚合样本失败: %v", err)
		return nil
	}
	defer rows.Close()
	samples := make([]Sample, 0)
	for rows.Next() {
		var sample Sample
		var ts int64
		if err := rows.Scan(&ts, &sample.Seq, &sample.CPUPercent, &sample.MemoryUsedBytes, &sample.MemoryTotalBytes, &sample.SwapUsedBytes, &sample.SwapTotalBytes, &sample.NetworkRxBPS, &sample.NetworkTxBPS, &sample.DiskReadBPS, &sample.DiskWriteBPS, &sample.TCPConnectionCount, &sample.UDPConnectionCount); err != nil {
			log.Printf("dashboard sqlite warning: 解析仪表板聚合样本失败: %v", err)
			return samples
		}
		sample.Time = time.Unix(ts, 0)
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		log.Printf("dashboard sqlite warning: 扫描仪表板聚合样本失败: %v", err)
	}
	return samples
}

func (c *Collector) querySQLiteHardware() ([]string, []string) {
	db := c.currentDB()
	if db == nil {
		return nil, nil
	}
	rows, err := db.Query(`SELECT kind, name FROM dashboard_devices ORDER BY kind ASC, name ASC`)
	if err != nil {
		log.Printf("dashboard sqlite warning: 查询硬件信息失败: %v", err)
		return nil, nil
	}
	defer rows.Close()
	interfaces := make([]string, 0)
	disks := make([]string, 0)
	for rows.Next() {
		var kind int64
		var name string
		if err := rows.Scan(&kind, &name); err != nil {
			log.Printf("dashboard sqlite warning: 解析硬件信息失败: %v", err)
			return interfaces, disks
		}
		if kind == dashboardDeviceKindNIC {
			interfaces = append(interfaces, name)
		} else if kind == dashboardDeviceKindDisk {
			disks = append(disks, name)
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("dashboard sqlite warning: 扫描硬件信息失败: %v", err)
	}
	return interfaces, disks
}

func mergeSortedStrings(left []string, right []string) []string {
	if len(left) == 0 {
		return append([]string(nil), right...)
	}
	if len(right) == 0 {
		return left
	}
	seen := make(map[string]struct{}, len(left)+len(right))
	result := make([]string, 0, len(left)+len(right))
	for _, value := range left {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	for _, value := range right {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
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
		log.Printf("dashboard sqlite warning: 查询设备聚合样本失败: %v", err)
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
			log.Printf("dashboard sqlite warning: 解析设备聚合样本失败: %v", err)
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

func (c *Collector) cleanupSQLite(maxDay int, compactAfterDay int) {
	if maxDay <= 0 {
		return
	}
	db := c.currentDB()
	if db == nil {
		return
	}
	if err := compactSQLite1sSamples(db, compactAfterDay); err != nil {
		log.Printf("dashboard sqlite warning: 压缩旧仪表板秒级样本失败: %v", err)
	}
	cutoff := beginningOfLocalDay(time.Now()).AddDate(0, 0, -maxDay+1).Unix()
	if _, err := db.Exec(`DELETE FROM dashboard_device_samples_1s WHERE ts < ?`, cutoff); err != nil {
		log.Printf("dashboard sqlite warning: 清理旧秒级设备样本失败: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM dashboard_samples_1s WHERE ts < ?`, cutoff); err != nil {
		log.Printf("dashboard sqlite warning: 清理旧秒级仪表板样本失败: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM dashboard_device_samples_1m WHERE bucket_ts < ?`, cutoff); err != nil {
		log.Printf("dashboard sqlite warning: 清理旧设备样本失败: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM dashboard_samples_1m WHERE bucket_ts < ?`, cutoff); err != nil {
		log.Printf("dashboard sqlite warning: 清理旧仪表板样本失败: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM dashboard_devices WHERE last_seen_ts < ?`, cutoff); err != nil {
		log.Printf("dashboard sqlite warning: 清理旧硬件信息失败: %v", err)
	}
	if _, err := db.Exec(`PRAGMA incremental_vacuum`); err != nil {
		log.Printf("dashboard sqlite warning: 回收仪表板数据库空间失败: %v", err)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		log.Printf("dashboard sqlite warning: 截断仪表板 WAL 失败: %v", err)
	}
}

func compactSQLite1sSamples(db *sql.DB, compactAfterDay int) error {
	if compactAfterDay <= 0 {
		return nil
	}
	cutoff := beginningOfLocalDay(time.Now()).AddDate(0, 0, -compactAfterDay).Unix()
	ctx, cancel := context.WithTimeout(context.Background(), sqliteConnectTimeout)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始压缩事务失败: %w", err)
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
WHERE ts < ?
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
WHERE ts < ?
GROUP BY bucket_ts, device_id
ON CONFLICT(device_id, bucket_ts) DO UPDATE SET
	sample_count = excluded.sample_count,
	read_avg = excluded.read_avg,
	write_avg = excluded.write_avg,
	read_max = excluded.read_max,
	write_max = excluded.write_max`,
		`DELETE FROM dashboard_device_samples_1s WHERE ts < ?`,
		`DELETE FROM dashboard_samples_1s WHERE ts < ?`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement, cutoff); err != nil {
			return fmt.Errorf("执行秒级样本压缩失败: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交压缩事务失败: %w", err)
	}
	committed = true
	return nil
}

func (c *Collector) cleanupCurrentSQLite() {
	c.mu.Lock()
	storageMode := c.storageMode
	maxDay := c.sqliteMaxDay
	compactAfterDay := c.sqliteCompactAfterDay
	c.mu.Unlock()
	if storageMode != storageModeSQLite {
		return
	}
	c.cleanupSQLite(maxDay, compactAfterDay)
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

type sampleKey struct{ unix, seq int64 }

func mergeHistorySamples(history []Sample, memory []Sample) []Sample {
	if len(history) == 0 {
		return memory
	}
	if len(memory) == 0 {
		return history
	}
	result := make([]Sample, 0, len(history)+len(memory))
	seen := make(map[sampleKey]struct{}, len(memory)*2)
	memoryStart := memory[0].Time
	for i := range history {
		if !history[i].Time.Before(memoryStart) {
			seen[sampleKey{unix: history[i].Time.Unix(), seq: history[i].Seq}] = struct{}{}
		}
		result = append(result, history[i])
	}
	for i := range memory {
		key := sampleKey{unix: memory[i].Time.Unix(), seq: memory[i].Seq}
		if _, ok := seen[key]; ok {
			continue
		}
		result = append(result, memory[i])
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Time.Equal(result[j].Time) {
			return result[i].Seq < result[j].Seq
		}
		return result[i].Time.Before(result[j].Time)
	})
	return result
}

func (c *Collector) calculateNetworkBandwidthLocked(now time.Time, counters map[string]networkCounter) (uint64, uint64, map[string]networkCounter) {
	interfaceCounters := make(map[string]networkCounter, len(counters))
	var totalRx, totalTx uint64
	seconds := now.Sub(c.lastNetworkTime).Seconds()
	for name, counter := range counters {
		var rxBPS, txBPS uint64
		previous, ok := c.lastNetwork[name]
		if ok && seconds > 0 {
			rxBPS = bytesPerSecond(counter.rxBytes, previous.rxBytes, seconds)
			txBPS = bytesPerSecond(counter.txBytes, previous.txBytes, seconds)
		}
		interfaceCounters[name] = networkCounter{rxBytes: rxBPS, txBytes: txBPS}
		totalRx += rxBPS
		totalTx += txBPS
	}
	c.lastNetwork = counters
	c.lastNetworkTime = now
	return totalRx, totalTx, interfaceCounters
}

func (c *Collector) calculateDiskBandwidthLocked(now time.Time, counters map[string]diskCounter) (uint64, uint64, map[string]diskCounter) {
	selectedDiskCounters := make(map[string]diskCounter, len(counters))
	var totalRead, totalWrite uint64
	seconds := now.Sub(c.lastDiskTime).Seconds()
	for name, counter := range counters {
		var readBPS, writeBPS uint64
		previous, ok := c.lastDisk[name]
		if ok && seconds > 0 {
			readBPS = bytesPerSecond(counter.readBytes, previous.readBytes, seconds)
			writeBPS = bytesPerSecond(counter.writeBytes, previous.writeBytes, seconds)
		}
		selectedDiskCounters[name] = diskCounter{readBytes: readBPS, writeBytes: writeBPS}
		totalRead += readBPS
		totalWrite += writeBPS
	}
	c.lastDisk = counters
	c.lastDiskTime = now
	return totalRead, totalWrite, selectedDiskCounters
}

func (c *Collector) trimLocked(now time.Time) {
	cutoff := now.Add(-dashboardMemoryRetention)
	keepFrom := 0
	for keepFrom < len(c.samples) && c.samples[keepFrom].Time.Before(cutoff) {
		keepFrom++
	}
	if keepFrom > 0 {
		copy(c.samples, c.samples[keepFrom:])
		c.samples = c.samples[:len(c.samples)-keepFrom]
	}
}

func (s Sample) public(nic string, diskName string) PublicSample {
	public := PublicSample{Time: s.Time, CPUPercent: s.CPUPercent, MemoryUsedBytes: s.MemoryUsedBytes, MemoryTotalBytes: s.MemoryTotalBytes, SwapUsedBytes: s.SwapUsedBytes, SwapTotalBytes: s.SwapTotalBytes, NetworkRxBPS: s.NetworkRxBPS, NetworkTxBPS: s.NetworkTxBPS, TCPConnectionCount: s.TCPConnectionCount, UDPConnectionCount: s.UDPConnectionCount, DiskReadBPS: s.DiskReadBPS, DiskWriteBPS: s.DiskWriteBPS}
	if nic != "" {
		counter, ok := s.interfaceCounters[nic]
		if !ok {
			public.NetworkRxBPS = 0
			public.NetworkTxBPS = 0
		} else {
			public.NetworkRxBPS = counter.rxBytes
			public.NetworkTxBPS = counter.txBytes
		}
	}
	if diskName != "" {
		counter, ok := s.diskCounters[diskName]
		if !ok {
			public.DiskReadBPS = 0
			public.DiskWriteBPS = 0
		} else {
			public.DiskReadBPS = counter.readBytes
			public.DiskWriteBPS = counter.writeBytes
		}
	}
	return public
}

func readNetworkCounters() (map[string]struct{}, map[string]networkCounter) {
	interfaces := make(map[string]struct{})
	counters := make(map[string]networkCounter)
	stats, err := net.IOCounters(true)
	if err != nil {
		log.Printf("collect dashboard network metrics failed: %v", err)
		return interfaces, counters
	}
	for i := range stats {
		name := strings.TrimSpace(stats[i].Name)
		if name == "" {
			continue
		}
		interfaces[name] = struct{}{}
		counters[name] = networkCounter{rxBytes: stats[i].BytesRecv, txBytes: stats[i].BytesSent}
	}
	return interfaces, counters
}
func readDiskCounters() (map[string]struct{}, map[string]diskCounter) {
	disks := make(map[string]struct{})
	counters := make(map[string]diskCounter)
	stats, err := disk.IOCounters()
	if err != nil {
		log.Printf("collect dashboard disk metrics failed: %v", err)
		return disks, counters
	}
	for name, stat := range stats {
		deviceName := strings.TrimSpace(name)
		if deviceName == "" {
			deviceName = strings.TrimSpace(stat.Name)
		}
		if deviceName == "" {
			continue
		}
		disks[deviceName] = struct{}{}
		counters[deviceName] = diskCounter{readBytes: stat.ReadBytes, writeBytes: stat.WriteBytes}
	}
	return disks, counters
}
func readConnectionCounts() (uint64, uint64) {
	tcpConnections, err := net.Connections("tcp")
	if err != nil {
		log.Printf("collect dashboard tcp connection metrics failed: %v", err)
	}
	udpConnections, err := net.Connections("udp")
	if err != nil {
		log.Printf("collect dashboard udp connection metrics failed: %v", err)
	}
	return uint64(len(tcpConnections)), uint64(len(udpConnections))
}

func sortedInterfacesLocked(interfaces map[string]struct{}) []string {
	result := make([]string, 0, len(interfaces))
	for name := range interfaces {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}
func sortedDisksLocked(disks map[string]struct{}) []string {
	result := make([]string, 0, len(disks))
	for name := range disks {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}
func normalizeRetentionMinutes(minutes int) int {
	if minutes < 1 {
		return 1
	}
	return minutes
}
func normalizeStorageMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == storageModeSQLite {
		return storageModeSQLite
	}
	return storageModeMemory
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
func normalizeSelectedMinutes(minutes int) int {
	if minutes <= 0 {
		return defaultMemoryMaxMin
	}
	return minutes
}
func normalizedFloat(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func downsampleSamples(samples []Sample, maxPoints int) []Sample {
	if maxPoints <= 0 || len(samples) <= maxPoints {
		return samples
	}
	if maxPoints <= 1 {
		return []Sample{samples[len(samples)-1]}
	}
	if maxPoints == 2 {
		return []Sample{samples[0], samples[len(samples)-1]}
	}
	start := samples[0].Time.Unix()
	end := samples[len(samples)-1].Time.Unix()
	if end <= start {
		return downsampleSamplesByIndex(samples, maxPoints)
	}
	bucketSeconds := int64((end - start + int64(maxPoints) - 1) / int64(maxPoints))
	if bucketSeconds <= 0 {
		bucketSeconds = 1
	}
	selected := make(map[int]struct{}, maxPoints*2)
	selected[0] = struct{}{}
	selected[len(samples)-1] = struct{}{}
	for startIndex := 0; startIndex < len(samples); {
		bucket := (samples[startIndex].Time.Unix() - start) / bucketSeconds
		endIndex := startIndex + 1
		for endIndex < len(samples) && (samples[endIndex].Time.Unix()-start)/bucketSeconds == bucket {
			endIndex++
		}
		for _, index := range representativeSampleIndexes(samples, startIndex, endIndex) {
			selected[index] = struct{}{}
		}
		startIndex = endIndex
	}
	indexes := make([]int, 0, len(selected))
	for index := range selected {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	result := make([]Sample, 0, len(indexes))
	for _, index := range indexes {
		result = append(result, samples[index])
	}
	if len(result) <= maxPoints {
		return result
	}
	return downsampleSamplesByIndex(result, maxPoints)
}
func representativeSampleIndexes(samples []Sample, startIndex int, endIndex int) []int {
	if endIndex-startIndex <= 1 {
		return []int{startIndex}
	}
	indexes := []int{startIndex, endIndex - 1}
	maxCPUIndex := startIndex
	maxNetworkIndex := startIndex
	maxDiskIndex := startIndex
	for i := startIndex; i < endIndex; i++ {
		if samples[i].CPUPercent > samples[maxCPUIndex].CPUPercent {
			maxCPUIndex = i
		}
		if samples[i].NetworkRxBPS+samples[i].NetworkTxBPS > samples[maxNetworkIndex].NetworkRxBPS+samples[maxNetworkIndex].NetworkTxBPS {
			maxNetworkIndex = i
		}
		if samples[i].DiskReadBPS+samples[i].DiskWriteBPS > samples[maxDiskIndex].DiskReadBPS+samples[maxDiskIndex].DiskWriteBPS {
			maxDiskIndex = i
		}
	}
	indexes = append(indexes, maxCPUIndex, maxNetworkIndex, maxDiskIndex)
	return indexes
}
func downsampleSamplesByIndex(samples []Sample, maxPoints int) []Sample {
	if len(samples) <= maxPoints {
		return samples
	}
	if maxPoints <= 1 {
		return []Sample{samples[len(samples)-1]}
	}
	step := float64(len(samples)-1) / float64(maxPoints-1)
	result := make([]Sample, 0, maxPoints)
	lastIndex := -1
	for i := 0; i < maxPoints; i++ {
		index := int(mathRound(float64(i) * step))
		if index <= lastIndex {
			index = lastIndex + 1
		}
		if index >= len(samples) {
			index = len(samples) - 1
		}
		result = append(result, samples[index])
		lastIndex = index
	}
	return result
}

func beginningOfLocalDay(t time.Time) time.Time {
	local := t.In(time.Local)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.Local)
}
func mathRound(value float64) int64 {
	if value < 0 {
		return int64(value - 0.5)
	}
	return int64(value + 0.5)
}
func bytesPerSecond(current uint64, previous uint64, seconds float64) uint64 {
	if current < previous || seconds <= 0 {
		return 0
	}
	return uint64(float64(current-previous) / seconds)
}
func minInt(a int, b int) int {
	if b <= 0 || a < b {
		return a
	}
	return b
}

func maxUint64(a uint64, b uint64) uint64 {
	if b > a {
		return b
	}
	return a
}

func maxFloat64(a float64, b float64) float64 {
	if b > a {
		return b
	}
	return a
}
