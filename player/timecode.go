package player

import (
	"strconv"
	"strings"
)

// Timecode renders a duration in milliseconds as m:ss.d, or h:mm:ss.d once it
// passes an hour.
//
// A tenth of a second is the right granularity here: frames are tens of
// milliseconds, so anything finer is noise, and anything coarser cannot show
// the difference a seek made. It is exported because every host wants to show
// the position, and two of them formatting it separately would be two chances
// to disagree about where the animation is.
func Timecode(ms uint64) string {
	tenths := (ms + 50) / 100 // nearest tenth
	hours := tenths / 36000
	minutes := (tenths / 600) % 60
	seconds := (tenths / 10) % 60

	var b strings.Builder
	if hours > 0 {
		b.WriteString(strconv.FormatUint(hours, 10))
		b.WriteByte(':')
		writeTwoDigits(&b, minutes)
	} else {
		b.WriteString(strconv.FormatUint(minutes, 10))
	}
	b.WriteByte(':')
	writeTwoDigits(&b, seconds)
	b.WriteByte('.')
	b.WriteString(strconv.FormatUint(tenths%10, 10))

	return b.String()
}

func writeTwoDigits(b *strings.Builder, v uint64) {
	if v < 10 {
		b.WriteByte('0')
	}
	b.WriteString(strconv.FormatUint(v, 10))
}

// Bar draws a progress bar of the given width. The fraction is clamped, so a
// caller may pass anything it has to hand.
//
// This lives beside Timecode for the same reason: it is presentation, and the
// component deliberately does not put either one in its box, so hosts draw them
// -- and hosts that draw the same bar should draw the same bar.
func Bar(fraction float64, width int) string {
	if width <= 0 {
		return ""
	}

	filled := int(clampFraction(fraction)*float64(width) + 0.5)
	filled = min(max(filled, 0), width)

	var b strings.Builder
	b.WriteByte('[')
	b.WriteString(strings.Repeat("#", filled))
	b.WriteString(strings.Repeat("-", width-filled))
	b.WriteByte(']')

	return b.String()
}

// clampFraction limits a fraction to 0..1, mapping NaN to 0 so that a bar built
// from an unknown position shows the start rather than nothing at all.
func clampFraction(fraction float64) float64 {
	switch {
	case fraction != fraction: // NaN
		return 0
	case fraction < 0:
		return 0
	case fraction > 1:
		return 1
	default:
		return fraction
	}
}
