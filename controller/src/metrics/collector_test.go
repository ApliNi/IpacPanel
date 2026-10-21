package metrics

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSamplePublic(t *testing.T) {
	sample := Sample{
		Time:         time.Unix(1700000000, 0),
		NetworkRxBPS: 1000,
		NetworkTxBPS: 2000,
		DiskReadBPS:  3000,
		DiskWriteBPS: 4000,
		interfaces: []deviceRate{
			{name: "eth0", read: 600, write: 1200},
			{name: "wlan0", read: 400, write: 800},
		},
		disks: []deviceRate{
			{name: "sda", read: 2000, write: 2500},
			{name: "nvme0n1", read: 1000, write: 1500},
		},
	}

	allPublic := sample.public("", "")
	if allPublic.NetworkRxBPS != 1000 || allPublic.NetworkTxBPS != 2000 {
		t.Fatalf("expected total net 1000/2000; got %d/%d", allPublic.NetworkRxBPS, allPublic.NetworkTxBPS)
	}
	if allPublic.DiskReadBPS != 3000 || allPublic.DiskWriteBPS != 4000 {
		t.Fatalf("expected total disk 3000/4000; got %d/%d", allPublic.DiskReadBPS, allPublic.DiskWriteBPS)
	}

	ethPublic := sample.public("eth0", "sda")
	if ethPublic.NetworkRxBPS != 600 || ethPublic.NetworkTxBPS != 1200 {
		t.Fatalf("expected eth0 600/1200; got %d/%d", ethPublic.NetworkRxBPS, ethPublic.NetworkTxBPS)
	}
	if ethPublic.DiskReadBPS != 2000 || ethPublic.DiskWriteBPS != 2500 {
		t.Fatalf("expected sda 2000/2500; got %d/%d", ethPublic.DiskReadBPS, ethPublic.DiskWriteBPS)
	}

	missingPublic := sample.public("unknown", "unknown")
	if missingPublic.NetworkRxBPS != 0 || missingPublic.NetworkTxBPS != 0 {
		t.Fatalf("expected unknown nic 0/0; got %d/%d", missingPublic.NetworkRxBPS, missingPublic.NetworkTxBPS)
	}
	if missingPublic.DiskReadBPS != 0 || missingPublic.DiskWriteBPS != 0 {
		t.Fatalf("expected unknown disk 0/0; got %d/%d", missingPublic.DiskReadBPS, missingPublic.DiskWriteBPS)
	}
}

func TestSQLiteConvergenceAndFlattenedStorage(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "metrics_sqlite_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test_dashboard.sqlite3")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("openSQLite failed: %v", err)
	}
	defer db.Close()

	var cacheSize int
	row := db.QueryRow("PRAGMA cache_size;")
	if err := row.Scan(&cacheSize); err != nil {
		t.Fatalf("failed to query cache_size: %v", err)
	}
	if cacheSize != -512 {
		t.Fatalf("expected cache_size -512; got %d", cacheSize)
	}

	now := time.Now().Truncate(time.Second)
	samples := []Sample{
		{
			Seq:                1,
			Time:               now,
			CPUPercent:         12.5,
			MemoryUsedBytes:    1024 * 1024 * 50,
			MemoryTotalBytes:   1024 * 1024 * 100,
			NetworkRxBPS:       1000,
			NetworkTxBPS:       2000,
			DiskReadBPS:        3000,
			DiskWriteBPS:       4000,
			TCPConnectionCount: 10,
			UDPConnectionCount: 5,
			interfaces: []deviceRate{
				{name: "eth0", read: 1000, write: 2000},
			},
			disks: []deviceRate{
				{name: "sda", read: 3000, write: 4000},
			},
		},
	}

	if err := writeSQLiteSamples(db, samples); err != nil {
		t.Fatalf("writeSQLiteSamples failed: %v", err)
	}

	collector := &Collector{
		enabled:     true,
		storageMode: storageModeSQLite,
		sqlitePath:  dbPath,
		db:          db,
		dbPath:      dbPath,
	}

	queried := collector.querySQLiteSamples(dbPath, 0, now.Add(-time.Minute), now.Add(time.Minute), 10, "eth0", "sda")
	if len(queried) == 0 {
		t.Fatalf("expected at least 1 queried sample; got 0")
	}

	public := queried[0].public("eth0", "sda")
	if public.NetworkRxBPS != 1000 || public.NetworkTxBPS != 2000 {
		t.Fatalf("expected eth0 1000/2000; got %d/%d", public.NetworkRxBPS, public.NetworkTxBPS)
	}
	if public.DiskReadBPS != 3000 || public.DiskWriteBPS != 4000 {
		t.Fatalf("expected sda 3000/4000; got %d/%d", public.DiskReadBPS, public.DiskWriteBPS)
	}
}

func BenchmarkSamplePublic(b *testing.B) {
	sample := Sample{
		Time:         time.Unix(1700000000, 0),
		NetworkRxBPS: 1000,
		NetworkTxBPS: 2000,
		DiskReadBPS:  3000,
		DiskWriteBPS: 4000,
		interfaces: []deviceRate{
			{name: "eth0", read: 600, write: 1200},
			{name: "eth1", read: 400, write: 800},
		},
		disks: []deviceRate{
			{name: "sda", read: 2000, write: 2500},
			{name: "sdb", read: 1000, write: 1500},
		},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = sample.public("eth0", "sda")
	}
}
