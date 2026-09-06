package web

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"os"
	"testing"
	"time"
)

func TestInspectAudioDurationAcceptsSupportedContainers(t *testing.T) {
	tests := []struct {
		name      string
		mediaType string
		data      []byte
	}{
		{name: "wav", mediaType: "audio/wav", data: wavFixture(time.Second)},
		{name: "ogg opus", mediaType: "audio/ogg", data: oggOpusFixture(time.Second)},
		{name: "webm opus", mediaType: "audio/webm", data: webMOpusFixture(time.Second)},
		{name: "streaming webm opus", mediaType: "audio/webm", data: webMUnknownClusterFixture(time.Second)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := audioFixtureFile(t, test.data)
			duration, err := inspectAudioDuration(file, test.mediaType)
			if err != nil {
				t.Fatal(err)
			}
			if difference := duration - time.Second; difference < -time.Millisecond || difference > time.Millisecond {
				t.Fatalf("duration = %v, want 1s", duration)
			}
		})
	}
}

func TestInspectAudioDurationRejectsMP4EvenWhenTimingTablesClaimValid(t *testing.T) {
	for name, data := range map[string][]byte{
		"one byte media payload claiming one second":  mp4AACFixture(time.Second),
		"fragmented timing table claiming one second": fragmentedMP4AACFixture(true),
	} {
		t.Run(name, func(t *testing.T) {
			file := audioFixtureFile(t, data)
			if _, err := inspectAudioDuration(file, "audio/mp4"); err == nil {
				t.Fatal("unverifiable MP4/AAC was accepted")
			}
		})
	}
}

func TestInspectAudioDurationRejectsMalformedContainers(t *testing.T) {
	tests := []struct {
		name      string
		mediaType string
		data      []byte
	}{
		{name: "truncated wav", mediaType: "audio/wav", data: []byte("RIFF\x20\x00\x00\x00WAVE")},
		{name: "wav trailing data", mediaType: "audio/wav", data: append(wavFixture(time.Second), 0)},
		{name: "wav duplicate format", mediaType: "audio/wav", data: wavDuplicateFormatFixture()},
		{name: "truncated ogg", mediaType: "audio/ogg", data: append([]byte(nil), oggOpusFixture(time.Second)[:30]...)},
		{name: "ogg without end marker", mediaType: "audio/ogg", data: oggWithoutEndFixture()},
		{name: "chained ogg stream", mediaType: "audio/ogg", data: chainedOggOpusFixture()},
		{name: "truncated webm", mediaType: "audio/webm", data: []byte{0x1a, 0x45, 0xdf, 0xa3, 0xff}},
		{name: "webm duplicate track number", mediaType: "audio/webm", data: webMOpusFixtureWithDuplicateTrackNumber(time.Second)},
		{name: "truncated mp4", mediaType: "audio/mp4", data: mp4BoxFixture("ftyp", []byte("M4A "))},
		{name: "mp4 duplicate media header", mediaType: "audio/mp4", data: mp4AACFixtureWithDuplicateMediaHeader(time.Second)},
		{name: "fragmented mp4 without decode time", mediaType: "audio/mp4", data: fragmentedMP4AACFixture(false)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := audioFixtureFile(t, test.data)
			if _, err := inspectAudioDuration(file, test.mediaType); err == nil {
				t.Fatalf("inspectAudioDuration(%s) accepted malformed input", test.mediaType)
			}
		})
	}
}

func TestCalculateOggChecksumMatchesReference(t *testing.T) {
	page := []byte("OggS\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00\x40\x30\x20\x10\x00\x00\x00\x00\x00\x00\x00\x00\x01\x04test")
	var expected uint32
	for index, value := range page {
		if index >= 22 && index < 26 {
			value = 0
		}
		expected ^= uint32(value) << 24
		for range 8 {
			if expected&0x80000000 != 0 {
				expected = expected<<1 ^ 0x04c11db7
			} else {
				expected <<= 1
			}
		}
	}
	if actual := calculateOggChecksum(page); actual != expected {
		t.Fatalf("checksum = %08x, want %08x", actual, expected)
	}
}

func TestMP4RunDurationRejectsUnreasonableSampleCountPromptly(t *testing.T) {
	data := make([]byte, 8)
	binary.BigEndian.PutUint32(data[4:8], math.MaxUint32)
	done := make(chan error, 1)
	go func() {
		items := 0
		_, err := mp4RunDuration(data, 1, math.MaxUint64, &items)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("unreasonable sample count was accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("sample-count validation exceeded its work bound")
	}
}

func TestInspectWebMClusterEnforcesSharedWorkBudget(t *testing.T) {
	payload := append(ebmlElementFixture([]byte{0xe7}, []byte{0}), ebmlElementFixture([]byte{0xa3}, []byte{0x81, 0, 0, 0, 0x08, 0})...)
	items := maximumAudioContainerItems - 1
	_, _, _, err := inspectWebMCluster(
		payload,
		ebmlElement{dataOffset: 0, end: len(payload)},
		webmTrack{number: 1, codec: "A_OPUS"},
		1_000_000,
		&items,
	)
	if err == nil {
		t.Fatal("WebM cluster exceeded the shared parser work budget")
	}
}

func TestReadWebMSegmentElementBoundsUnknownClusterAtNextSibling(t *testing.T) {
	firstPayload := append(ebmlElementFixture([]byte{0xe7}, []byte{0}), ebmlElementFixture([]byte{0xa3}, []byte{0x81, 0, 0, 0, 0x08, 0})...)
	data := append([]byte{0x1f, 0x43, 0xb6, 0x75, 0xff}, firstPayload...)
	expectedNext := len(data)
	secondPayload := ebmlElementFixture([]byte{0xe7}, []byte{20})
	data = append(data, ebmlElementFixture([]byte{0x1f, 0x43, 0xb6, 0x75}, secondPayload)...)
	items := 0
	first, next, err := readWebMSegmentElement(data, 0, len(data), &items)
	if err != nil || first.end != expectedNext || next != expectedNext {
		t.Fatalf("first cluster end/next/error = %d/%d/%v, want %d/%d/nil", first.end, next, err, expectedNext, expectedNext)
	}
	second, final, err := readWebMSegmentElement(data, next, len(data), &items)
	if err != nil || second.id != 0x1f43b675 || final != len(data) {
		t.Fatalf("second cluster id/final/error = %x/%d/%v", second.id, final, err)
	}
}

func TestVoiceDurationsMatchAllowsCaptureJitterButRejectsDishonestValue(t *testing.T) {
	if !voiceDurationsMatch(89*time.Second, 90*time.Second) {
		t.Fatal("one-second recorder jitter was rejected")
	}
	if voiceDurationsMatch(time.Second, 90*time.Second) {
		t.Fatal("materially dishonest duration was accepted")
	}
}

func FuzzInspectAudioDuration(f *testing.F) {
	for _, seed := range []struct {
		mediaType string
		data      []byte
	}{
		{mediaType: "audio/wav", data: wavFixture(time.Second)},
		{mediaType: "audio/ogg", data: oggOpusFixture(time.Second)},
		{mediaType: "audio/webm", data: webMUnknownClusterFixture(time.Second)},
		{mediaType: "audio/mp4", data: fragmentedMP4AACFixture(true)},
		{mediaType: "application/octet-stream", data: []byte("invalid")},
	} {
		f.Add(seed.mediaType, seed.data)
	}
	directory := f.TempDir()
	f.Fuzz(func(t *testing.T, mediaType string, data []byte) {
		if len(mediaType) > 64 || len(data) > 1<<20 {
			return
		}
		file, err := os.CreateTemp(directory, "audio-fuzz-*")
		if err != nil {
			t.Fatal(err)
		}
		name := file.Name()
		defer func() {
			_ = file.Close()
			_ = os.Remove(name)
		}()
		if _, err = file.Write(data); err != nil {
			t.Fatal(err)
		}
		_, _ = inspectAudioDuration(file, mediaType)
	})
}

func FuzzOggDuration(f *testing.F) {
	f.Add(oggOpusFixture(time.Second))
	f.Add([]byte("OggS"))
	f.Fuzz(func(_ *testing.T, data []byte) {
		if len(data) <= 1<<20 {
			_, _ = oggDuration(data)
		}
	})
}

func FuzzWebMDuration(f *testing.F) {
	f.Add(webMOpusFixture(time.Second))
	f.Add(webMUnknownClusterFixture(time.Second))
	f.Add([]byte{0x1a, 0x45, 0xdf, 0xa3})
	f.Fuzz(func(_ *testing.T, data []byte) {
		if len(data) <= 1<<20 {
			_, _ = webmDuration(data)
		}
	})
}

func FuzzMP4Duration(f *testing.F) {
	f.Add(mp4AACFixture(time.Second))
	f.Add(fragmentedMP4AACFixture(true))
	f.Add([]byte("ftyp"))
	f.Fuzz(func(_ *testing.T, data []byte) {
		if len(data) <= 1<<20 {
			_, _ = mp4Duration(data)
		}
	})
}

func audioFixtureFile(t *testing.T, data []byte) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "audio-*.fixture")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if _, err = file.Write(data); err != nil {
		t.Fatal(err)
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	return file
}

func fixtureUint32(value int) uint32 {
	if value < 0 || int64(value) > math.MaxUint32 {
		panic("fixture value does not fit uint32")
	}
	//nolint:gosec // The fixture conversion is explicitly bounded above.
	return uint32(value)
}

func fixtureByte(value int) byte {
	if value < 0 || value > math.MaxUint8 {
		panic("fixture value does not fit byte")
	}
	//nolint:gosec // The fixture conversion is explicitly bounded above.
	return byte(value)
}

func wavFixture(duration time.Duration) []byte {
	const sampleRate = uint32(8000)
	const bytesPerSecond = sampleRate
	audioBytes := uint32(duration.Seconds() * float64(bytesPerSecond))
	data := make([]byte, 44+audioBytes)
	copy(data[:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], fixtureUint32(len(data)-8))
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], sampleRate)
	binary.LittleEndian.PutUint32(data[28:32], bytesPerSecond)
	binary.LittleEndian.PutUint16(data[32:34], 1)
	binary.LittleEndian.PutUint16(data[34:36], 8)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], audioBytes)
	return data
}

func wavDuplicateFormatFixture() []byte {
	original := wavFixture(time.Second)
	data := append([]byte(nil), original[:36]...)
	data = append(data, original[12:36]...)
	data = append(data, original[36:]...)
	binary.LittleEndian.PutUint32(data[4:8], fixtureUint32(len(data)-8))
	return data
}

func oggOpusFixture(duration time.Duration) []byte {
	return oggOpusFixtureWithSerial(duration, 0x10203040)
}

func oggOpusFixtureWithSerial(duration time.Duration, serial uint32) []byte {
	const samplesPerPacket = uint64(960)
	packets := int(duration * 48000 / time.Second / time.Duration(samplesPerPacket))
	result := oggPageFixture(serial, 0, 2, 0, []byte("OpusHead\x01\x01\x00\x00\x80\xbb\x00\x00\x00\x00\x00"))
	result = append(result, oggPageFixture(serial, 1, 0, 0, []byte("OpusTags\x00\x00\x00\x00\x00\x00\x00\x00"))...)
	for index := 0; index < packets; index++ {
		headerType := byte(0)
		if index == packets-1 {
			headerType = 4
		}
		granule := uint64(index+1) * samplesPerPacket
		result = append(result, oggPageFixture(serial, uint32(index+2), headerType, granule, []byte{0x08, 0})...)
	}
	return result
}

func chainedOggOpusFixture() []byte {
	data := oggOpusFixtureWithSerial(time.Second, 0x10203040)
	return append(data, oggOpusFixtureWithSerial(time.Second, 0x50607080)...)
}

func oggWithoutEndFixture() []byte {
	data := oggOpusFixture(time.Second)
	lastPage := 0
	for offset := 0; offset < len(data); {
		lastPage = offset
		segments := int(data[offset+26])
		payloadSize := 0
		for _, size := range data[offset+27 : offset+27+segments] {
			payloadSize += int(size)
		}
		offset += 27 + segments + payloadSize
	}
	data[lastPage+5] &^= 4
	binary.LittleEndian.PutUint32(data[lastPage+22:lastPage+26], calculateOggChecksum(data[lastPage:]))
	return data
}

func oggPageFixture(serial, sequence uint32, headerType byte, granule uint64, packet []byte) []byte {
	page := make([]byte, 28+len(packet))
	copy(page[:4], "OggS")
	page[5] = headerType
	binary.LittleEndian.PutUint64(page[6:14], granule)
	binary.LittleEndian.PutUint32(page[14:18], serial)
	binary.LittleEndian.PutUint32(page[18:22], sequence)
	page[26] = 1
	page[27] = fixtureByte(len(packet))
	copy(page[28:], packet)
	binary.LittleEndian.PutUint32(page[22:26], calculateOggChecksum(page))
	return page
}

func webMOpusFixture(duration time.Duration) []byte {
	return webMOpusFixtureWithOptions(duration, false)
}

func webMOpusFixtureWithDuplicateTrackNumber(duration time.Duration) []byte {
	return webMOpusFixtureWithOptions(duration, true)
}

func webMOpusFixtureWithOptions(duration time.Duration, duplicateTrackNumber bool) []byte {
	info := append(
		ebmlElementFixture([]byte{0x2a, 0xd7, 0xb1}, []byte{0x0f, 0x42, 0x40}),
		ebmlElementFixture([]byte{0x44, 0x89}, float64Fixture(duration.Seconds()*1000))...,
	)
	entry := append(ebmlElementFixture([]byte{0xd7}, []byte{1}), ebmlElementFixture([]byte{0x83}, []byte{2})...)
	if duplicateTrackNumber {
		entry = append(entry, ebmlElementFixture([]byte{0xd7}, []byte{2})...)
	}
	entry = append(entry, ebmlElementFixture([]byte{0x86}, []byte("A_OPUS"))...)
	entry = append(entry, ebmlElementFixture([]byte{0x23, 0xe3, 0x83}, []byte{0x01, 0x31, 0x2d, 0x00})...)
	tracks := ebmlElementFixture([]byte{0xae}, entry)
	cluster := ebmlElementFixture([]byte{0xe7}, []byte{0})
	packetCount := int(duration / (20 * time.Millisecond))
	for index := 0; index < packetCount; index++ {
		block := []byte{0x81, byte(index * 20 >> 8), byte(index * 20), 0, 0x08, 0}
		cluster = append(cluster, ebmlElementFixture([]byte{0xa3}, block)...)
	}
	segment := append(ebmlElementFixture([]byte{0x15, 0x49, 0xa9, 0x66}, info), ebmlElementFixture([]byte{0x16, 0x54, 0xae, 0x6b}, tracks)...)
	segment = append(segment, ebmlElementFixture([]byte{0x1f, 0x43, 0xb6, 0x75}, cluster)...)
	return append(ebmlElementFixture([]byte{0x1a, 0x45, 0xdf, 0xa3}, nil), ebmlElementFixture([]byte{0x18, 0x53, 0x80, 0x67}, segment)...)
}

func webMUnknownClusterFixture(duration time.Duration) []byte {
	data := webMOpusFixture(duration)
	clusterID := []byte{0x1f, 0x43, 0xb6, 0x75}
	clusterOffset := bytes.Index(data, clusterID)
	if clusterOffset < 0 {
		panic("WebM fixture has no cluster")
	}
	_, width, _, err := readEBMLVariable(data, clusterOffset+len(clusterID), 8, true)
	if err != nil {
		panic("WebM fixture has an invalid cluster size")
	}
	sizeOffset := clusterOffset + len(clusterID)
	data[sizeOffset] = ^byte(0) >> (width - 1)
	for index := 1; index < width; index++ {
		data[sizeOffset+index] = 0xff
	}
	return data
}

func ebmlElementFixture(id, payload []byte) []byte {
	result := append([]byte(nil), id...)
	result = append(result, ebmlSizeFixture(uint64(len(payload)))...)
	return append(result, payload...)
}

func ebmlSizeFixture(size uint64) []byte {
	for width := 1; width <= 8; width++ {
		maximum := uint64(1)<<(7*width) - 2
		if size <= maximum {
			result := make([]byte, width)
			value := size | uint64(1)<<(7*width)
			for index := width - 1; index >= 0; index-- {
				result[index] = byte(value)
				value >>= 8
			}
			return result
		}
	}
	panic("fixture is too large")
}

func float64Fixture(value float64) []byte {
	result := make([]byte, 8)
	binary.BigEndian.PutUint64(result, math.Float64bits(value))
	return result
}

func mp4AACFixture(duration time.Duration) []byte {
	return mp4AACFixtureWithOptions(duration, false)
}

func mp4AACFixtureWithDuplicateMediaHeader(duration time.Duration) []byte {
	return mp4AACFixtureWithOptions(duration, true)
}

func mp4AACFixtureWithOptions(duration time.Duration, duplicateMediaHeader bool) []byte {
	const timeScale = uint32(48000)
	units := uint32(duration.Seconds() * float64(timeScale))
	tkhd := make([]byte, 84)
	binary.BigEndian.PutUint32(tkhd[12:16], 1)
	mdhd := make([]byte, 24)
	binary.BigEndian.PutUint32(mdhd[12:16], timeScale)
	binary.BigEndian.PutUint32(mdhd[16:20], units)
	hdlr := make([]byte, 12)
	copy(hdlr[8:12], "soun")
	stts := make([]byte, 16)
	binary.BigEndian.PutUint32(stts[4:8], 1)
	binary.BigEndian.PutUint32(stts[8:12], 50)
	binary.BigEndian.PutUint32(stts[12:16], units/50)
	stbl := mp4BoxFixture("stts", stts)
	minf := mp4BoxFixture("stbl", stbl)
	mdia := append(mp4BoxFixture("mdhd", mdhd), mp4BoxFixture("hdlr", hdlr)...)
	if duplicateMediaHeader {
		mdia = append(mdia, mp4BoxFixture("mdhd", mdhd)...)
	}
	mdia = append(mdia, mp4BoxFixture("minf", minf)...)
	trak := append(mp4BoxFixture("tkhd", tkhd), mp4BoxFixture("mdia", mdia)...)
	result := append(mp4BoxFixture("ftyp", []byte("M4A \x00\x00\x00\x00M4A ")), mp4BoxFixture("moov", mp4BoxFixture("trak", trak))...)
	return append(result, mp4BoxFixture("mdat", []byte{0})...)
}

func fragmentedMP4AACFixture(includeDecodeTime bool) []byte {
	const timeScale = uint32(48000)
	tkhd := make([]byte, 84)
	binary.BigEndian.PutUint32(tkhd[12:16], 1)
	mdhd := make([]byte, 24)
	binary.BigEndian.PutUint32(mdhd[12:16], timeScale)
	hdlr := make([]byte, 12)
	copy(hdlr[8:12], "soun")
	mdia := append(mp4BoxFixture("mdhd", mdhd), mp4BoxFixture("hdlr", hdlr)...)
	trak := append(mp4BoxFixture("tkhd", tkhd), mp4BoxFixture("mdia", mdia)...)

	tfhd := make([]byte, 12)
	tfhd[3] = 0x08
	binary.BigEndian.PutUint32(tfhd[4:8], 1)
	binary.BigEndian.PutUint32(tfhd[8:12], 960)
	trun := make([]byte, 8)
	binary.BigEndian.PutUint32(trun[4:8], 50)
	traf := mp4BoxFixture("tfhd", tfhd)
	if includeDecodeTime {
		traf = append(traf, mp4BoxFixture("tfdt", make([]byte, 8))...)
	}
	traf = append(traf, mp4BoxFixture("trun", trun)...)
	moof := mp4BoxFixture("traf", traf)

	result := append(mp4BoxFixture("ftyp", []byte("M4A \x00\x00\x00\x00M4A ")), mp4BoxFixture("moov", mp4BoxFixture("trak", trak))...)
	result = append(result, mp4BoxFixture("moof", moof)...)
	return append(result, mp4BoxFixture("mdat", []byte{0})...)
}

func mp4BoxFixture(boxType string, payload []byte) []byte {
	result := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(result[:4], fixtureUint32(len(result)))
	copy(result[4:8], boxType)
	copy(result[8:], payload)
	return result
}
