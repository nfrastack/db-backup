// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package license

var (
	keySeedA = []byte{
		0x97, 0x44, 0xa0, 0xf7, 0x74, 0xdc, 0x29, 0x6d,
		0xe0, 0x7e, 0x3d, 0x8f, 0xfd, 0x94, 0x0d, 0x40,
	}
	keySeedB = []byte{
		0x14, 0xca, 0x5e, 0x8d, 0xd3, 0x10, 0x8d, 0xf1,
		0xac, 0xe4, 0xf7, 0x48, 0xef, 0x37, 0x95, 0xa8,
	}
	maskSeed = []byte{
		0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8,
		0xa9, 0xaa, 0xab, 0xac, 0xad, 0xae, 0xaf, 0xb0,
	}
)

var keySaltBytes = []byte("db-backup/features/v1")

var (
	saltEncryption  = mustHex("12839fb6a7572d0163f30cd8")
	saltIncremental = mustHex("38d2598918f9c1032df540b0")
	saltStorage     = mustHex("8951c810626a")
	saltArchive     = mustHex("73bc88002a98")
	saltRetention   = mustHex("9168b0706b86")
	saltMaintenance = mustHex("6ef5d652a1a9")
	saltSchedule    = mustHex("a1b2c304d5e6f7081920a3b4")
	saltRestore     = mustHex("c4d5e6f7081920a3b4c5d6e7")
	saltNotif       = mustHex("f0e1d2c3b4a5968778695a4b")
)

var featureSalts = map[string][]byte{
	FeatureEncryption:    saltEncryption,
	FeatureIncremental:   saltIncremental,
	FeatureStorage:       saltStorage,
	FeatureArchive:       saltArchive,
	FeatureRetention:     saltRetention,
	FeatureMaintenance:   saltMaintenance,
	FeatureSchedule:      saltSchedule,
	FeatureRestore:       saltRestore,
	FeatureNotifications: saltNotif,
}

var publicKeys = map[string][]byte{
	"nfrastack-2026-01": assembleKey(),
}

func assembleKey() []byte {
	out := make([]byte, 32)
	for i := 0; i < 16; i++ {
		out[i] = keySeedA[i] ^ maskSeed[i]
		out[i+16] = keySeedB[i] ^ maskSeed[i]
	}
	return out
}

func featureSalt(feature string) []byte {
	if s, ok := featureSalts[feature]; ok {
		return s
	}
	return []byte(feature)
}

func hexVal(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}
func keySalt() []byte { return keySaltBytes }

func mustHex(s string) []byte {
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		hi := hexVal(s[i*2])
		lo := hexVal(s[i*2+1])
		out[i] = hi<<4 | lo
	}
	return out
}
