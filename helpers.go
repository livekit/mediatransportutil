package mediatransportutil

import (
	"math"
	"time"

	"github.com/pion/rtcp"
)

var (
	ntpEpoch = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
)

type NtpTime uint64

func (t NtpTime) Duration() time.Duration {
	sec := (t >> 32) * 1e9
	frac := (t & 0xffffffff) * 1e9
	nsec := frac >> 32
	if uint32(frac) >= 0x80000000 {
		nsec++
	}
	return time.Duration(sec + nsec)
}

func (t NtpTime) Time() time.Time {
	return ntpEpoch.Add(t.Duration())
}

func ToNtpTime(t time.Time) NtpTime {
	nsec := uint64(t.Sub(ntpEpoch))
	sec := nsec / 1e9
	nsec = (nsec - sec*1e9) << 32
	frac := nsec / 1e9
	if nsec%1e9 >= 1e9/2 {
		frac++
	}
	return NtpTime(sec<<32 | frac)
}

// ------------------------------------------

func GetRttMs(report *rtcp.ReceptionReport) uint32 {
	if report.LastSenderReport == 0 {
		return 0
	}

	// RTT calculation reference: https://datatracker.ietf.org/doc/html/rfc3550#section-6.4.1

	// middle 32-bits of current NTP time
	now := uint32(ToNtpTime(time.Now()) >> 16)
	if now < (report.LastSenderReport + report.Delay) {
		return 0
	}
	ntpDiff := now - report.LastSenderReport - report.Delay
	return uint32(math.Ceil(float64(ntpDiff) * 1000.0 / 65536.0))
}
