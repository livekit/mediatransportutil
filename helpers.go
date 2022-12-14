package mediatransportutil

import (
	"errors"
	"fmt"
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

func getRttMs(report *rtcp.ReceptionReport, lastSRNTP NtpTime, lastSentAt time.Time, ignoreLast bool) (uint32, error) {
	if report.LastSenderReport == 0 {
		return 0, errors.New("no last SR")
	}

	if !ignoreLast && report.LastSenderReport != uint32((lastSRNTP>>16)&0xFFFFFFFF) {
		return 0, fmt.Errorf("not last SR, lastSR: 0x%x, received: 0x%x", uint32((lastSRNTP>>16)&0xFFFFFFFF), report.LastSenderReport)
	}

	if !ignoreLast && lastSentAt.IsZero() {
		return 0, errors.New("no last send time")
	}

	// RTT calculation reference: https://datatracker.ietf.org/doc/html/rfc3550#section-6.4.1

	// middle 32-bits of current NTP time
	var nowNTP uint32
	timeSinceLastSR := time.Since(lastSentAt)
	if !ignoreLast {
		nowNTP = uint32(ToNtpTime(lastSRNTP.Time().Add(timeSinceLastSR)) >> 16)
	} else {
		nowNTP = uint32(ToNtpTime(time.Now()) >> 16)
	}
	if nowNTP < (report.LastSenderReport + report.Delay) {
		return 0, fmt.Errorf("anachronous, nowNTP: %d, lsr: %d, delay: %d, lastSentAt: %+v, since: %+v", nowNTP, report.LastSenderReport, report.Delay, lastSentAt, timeSinceLastSR)
	}

	ntpDiff := nowNTP - report.LastSenderReport - report.Delay
	return uint32(math.Ceil(float64(ntpDiff) * 1000.0 / 65536.0)), nil
}

func GetRttMs(report *rtcp.ReceptionReport, lastSRNTP NtpTime, lastSentAt time.Time) (uint32, error) {
	return getRttMs(report, lastSRNTP, lastSentAt, false)
}

func GetRttMsFromReceiverReportOnly(report *rtcp.ReceptionReport) (uint32, error) {
	return getRttMs(report, 0, time.Time{}, true)
}
