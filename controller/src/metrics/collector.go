package metrics

import (
	"IpacPanel/controller/src/metrics/sysnet"
	"IpacPanel/controller/src/msg"
	"database/sql"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

const (
	sampleInterval           = time.Second
	dashboardMemoryRetention = 30 * time.Second
	sqliteFlushInterval      = 30 * time.Second
	sqliteCleanupInterval    = time.Hour
	defaultMemoryMaxMin      = 30
	storageModeMemory        = "memory"
	storageModeSQLite        = "sqlite"
	dashboardDeviceNIC       = "nic"
	dashboardDeviceDisk      = "disk"
)

type Config struct {
	Enabled               bool
	StorageMode           string
	MemoryMaxMin          int
	SQLiteMaxDay          int
	SQLiteCompactAfterDay int
	SQLitePath            string
	DeviceFilter          []string
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

type deviceFilter struct {
	includes map[string]struct{}
	excludes map[string]struct{}
}

func compileDeviceFilter(rules []string) deviceFilter {
	filter := deviceFilter{
		includes: make(map[string]struct{}),
		excludes: make(map[string]struct{}),
	}
	for _, rule := range rules {
		name := strings.TrimSpace(rule)
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, "!") {
			excluded := strings.TrimSpace(strings.TrimPrefix(name, "!"))
			if excluded != "" {
				filter.excludes[strings.ToLower(excluded)] = struct{}{}
			}
			continue
		}
		filter.includes[strings.ToLower(name)] = struct{}{}
	}
	return filter
}

func (f deviceFilter) allows(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if len(f.includes) > 0 {
		if _, ok := f.includes[normalized]; !ok {
			return false
		}
	}
	if _, ok := f.excludes[normalized]; ok {
		return false
	}
	return true
}

func (f deviceFilter) filterInterfaces(values map[string]struct{}, counters map[string]networkCounter) (map[string]struct{}, map[string]networkCounter) {
	if len(f.includes) == 0 && len(f.excludes) == 0 {
		return values, counters
	}
	filteredValues := make(map[string]struct{}, len(values))
	filteredCounters := make(map[string]networkCounter, len(counters))
	for name := range values {
		if f.allows(name) {
			filteredValues[name] = struct{}{}
			if counter, ok := counters[name]; ok {
				filteredCounters[name] = counter
			}
		}
	}
	return filteredValues, filteredCounters
}

func (f deviceFilter) filterDisks(values map[string]struct{}, counters map[string]diskCounter) (map[string]struct{}, map[string]diskCounter) {
	if len(f.includes) == 0 && len(f.excludes) == 0 {
		return values, counters
	}
	filteredValues := make(map[string]struct{}, len(values))
	filteredCounters := make(map[string]diskCounter, len(counters))
	for name := range values {
		if f.allows(name) {
			filteredValues[name] = struct{}{}
			if counter, ok := counters[name]; ok {
				filteredCounters[name] = counter
			}
		}
	}
	return filteredValues, filteredCounters
}

func (f deviceFilter) filterSortedStrings(values []string) []string {
	if len(values) == 0 || (len(f.includes) == 0 && len(f.excludes) == 0) {
		return values
	}
	result := make([]string, 0, len(values))
	for _, name := range values {
		if f.allows(name) {
			result = append(result, name)
		}
	}
	return result
}

type diskCounter struct {
	readBytes  uint64
	writeBytes uint64
}


type Collector struct {
	mu                    sync.Mutex
	sqliteMu              sync.Mutex
	tcpCount              atomic.Uint64
	udpCount              atomic.Uint64
	enabled               bool
	retentionMinutes      int
	storageMode           string
	sqliteMaxDay          int
	sqliteCompactAfterDay int
	sqlitePath            string
	sqliteGeneration      uint64
	deviceFilter          deviceFilter
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
		deviceFilter:          compileDeviceFilter(config.DeviceFilter),
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
	c.deviceFilter = compileDeviceFilter(config.DeviceFilter)
	pathChanged := previousSQLitePath != sqlitePath || previousStorageMode != storageMode
	c.sqliteGeneration++
	sqliteGeneration := c.sqliteGeneration
	if !config.Enabled || retentionMinutes <= 0 {
		c.enabled = false
		c.samples = nil
		pending := c.takeSQLitePendingLocked()
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
			c.writeSQLiteSamplesToStandalonePath(previousSQLitePath, pending)
		}
		c.closeSQLite()
		return
	}
	if c.enabled {
		var previousPending []Sample
		if previousStorageMode == storageModeSQLite && pathChanged {
			previousPending = c.takeSQLitePendingLocked()
		}
		c.trimLocked(time.Now())
		c.mu.Unlock()
		if previousStorageMode == storageModeSQLite && pathChanged {
			c.writeSQLiteSamplesToStandalonePath(previousSQLitePath, previousPending)
		}
		if storageMode == storageModeSQLite {
			c.startSQLiteMaintenance(sqlitePath, sqliteMaxDay, sqliteCompactAfterDay, sqliteGeneration)
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
		c.startSQLiteMaintenance(sqlitePath, sqliteMaxDay, sqliteCompactAfterDay, sqliteGeneration)
	}
	go c.run(stopCh, doneCh)
}

func (c *Collector) Stop() {
	c.mu.Lock()
	stopCh := c.stopCh
	doneCh := c.doneCh
	storageMode := c.storageMode
	sqlitePath := c.sqlitePath
	pending := c.takeSQLitePendingLocked()
	c.sqliteGeneration++
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
		c.writeSQLiteSamplesToStandalonePath(sqlitePath, pending)
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
	sqlitePath := c.sqlitePath
	sqliteGeneration := c.sqliteGeneration
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
		storedInterfaces, storedDisks := c.querySQLiteHardware(sqlitePath, sqliteGeneration)
		interfaces = mergeSortedStrings(interfaces, storedInterfaces)
		disks = mergeSortedStrings(disks, storedDisks)
	}
	c.mu.Lock()
	deviceFilter := c.deviceFilter
	c.mu.Unlock()
	interfaces = deviceFilter.filterSortedStrings(interfaces)
	disks = deviceFilter.filterSortedStrings(disks)

	var samples []Sample
	if enabled && storageMode == storageModeSQLite {
		queryTo := now
		if len(memory) > 0 {
			queryTo = memory[0].Time.Add(-time.Second)
		}
		samples = c.querySQLiteSamples(sqlitePath, sqliteGeneration, cutoff, queryTo, maxPoints, nic, disk)
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
	sqlitePath := c.sqlitePath
	sqliteGeneration := c.sqliteGeneration
	interfaces := sortedInterfacesLocked(c.interfaces)
	disks := sortedDisksLocked(c.disks)
	c.mu.Unlock()
	if enabled && storageMode == storageModeSQLite {
		storedInterfaces, storedDisks := c.querySQLiteHardware(sqlitePath, sqliteGeneration)
		interfaces = mergeSortedStrings(interfaces, storedInterfaces)
		disks = mergeSortedStrings(disks, storedDisks)
	}
	c.mu.Lock()
	deviceFilter := c.deviceFilter
	c.mu.Unlock()
	interfaces = deviceFilter.filterSortedStrings(interfaces)
	disks = deviceFilter.filterSortedStrings(disks)
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
	c.updateSocketCounts()
	go c.runSocketWorker(stopCh)
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
		log.Printf(msg.DashboardCollectCPUMetricsFailedLogFmt, err)
	}

	virtualMemory, err := mem.VirtualMemory()
	if err != nil {
		log.Printf(msg.DashboardCollectMemoryMetricsFailedLogFmt, err)
		virtualMemory = &mem.VirtualMemoryStat{}
	}
	swapMemory, err := mem.SwapMemory()
	if err != nil {
		log.Printf(msg.DashboardCollectSwapMetricsFailedLogFmt, err)
		swapMemory = &mem.SwapMemoryStat{}
	}
	interfaces, networkCounters := readNetworkCounters()
	tcpConnectionCount := c.tcpCount.Load()
	udpConnectionCount := c.udpCount.Load()
	disks, diskCounters := readDiskCounters()

	c.mu.Lock()
	if !c.enabled {
		c.mu.Unlock()
		return
	}
	interfaces, networkCounters = c.deviceFilter.filterInterfaces(interfaces, networkCounters)
	disks, diskCounters = c.deviceFilter.filterDisks(disks, diskCounters)
	networkRxBPS, networkTxBPS, interfaceCounters := c.calculateNetworkBandwidthLocked(now, networkCounters)
	diskReadBPS, diskWriteBPS, selectedDiskCounters := c.calculateDiskBandwidthLocked(now, diskCounters)
	c.interfaces = make(map[string]struct{}, len(interfaces))
	for name := range interfaces {
		c.interfaces[name] = struct{}{}
	}
	c.disks = make(map[string]struct{}, len(disks))
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
		log.Printf(msg.DashboardCollectNetworkMetricsFailedLogFmt, err)
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
		log.Printf(msg.DashboardCollectDiskMetricsFailedLogFmt, err)
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
func (c *Collector) updateSocketCounts() {
	counts, err := sysnet.ReadSocketCounts()
	if err != nil {
		log.Printf(msg.DashboardCollectSocketMetricsFailedLogFmt, err)
		return
	}
	c.tcpCount.Store(counts.TCP)
	c.udpCount.Store(counts.UDP)
}

func (c *Collector) runSocketWorker(stopCh <-chan struct{}) {
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			c.updateSocketCounts()
		}
	}
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
func normalizeSelectedMinutes(minutes int) int {
	if minutes <= 0 {
		return defaultMemoryMaxMin
	}
	return minutes
}
