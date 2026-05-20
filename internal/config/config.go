package config

import "time"

type Config struct {
	DefaultP2PTTL       time.Duration
	DefaultMaxDownloads int
	RequireE2E          bool
	Transport           string
}

func Defaults() Config {
	return Config{
		DefaultP2PTTL:       30 * time.Minute,
		DefaultMaxDownloads: 1,
		RequireE2E:          true,
		Transport:           "webrtc_p2p",
	}
}
