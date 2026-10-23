package instancefs

import "testing"

func TestIsWindowsReservedDeviceName(t *testing.T) {
	reserved := []string{
		"CON", "con", "prn", "AUX", "nul",
		"CONIN$", "conin$", "CONOUT$", "conout$", "CLOCK$", "clock$",
		"COM1", "com9", "com0", "LPT1", "lpt9",
		"con.txt", "CONIN$.log", "aux.tar.gz",
	}
	for _, name := range reserved {
		if !IsWindowsReservedDeviceName(name) {
			t.Errorf("expected %q to be recognized as reserved Windows device name", name)
		}
	}

	notReserved := []string{
		"connect", "content.txt", "printer", "clockwork", "com", "lpt",
	}
	for _, name := range notReserved {
		if IsWindowsReservedDeviceName(name) {
			t.Errorf("expected %q NOT to be recognized as reserved Windows device name", name)
		}
	}
}
