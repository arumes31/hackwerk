package web

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"time"
)

var errInvalidAudioContainer = errors.New("invalid audio container")

const (
	maximumInspectedAudioDuration = 24 * time.Hour
	maximumInspectedAudioBytes    = 50 << 20
	maximumAudioContainerItems    = 250_000
)

func consumeAudioContainerItem(items *int) error {
	if *items >= maximumAudioContainerItems {
		return errInvalidAudioContainer
	}
	*items = *items + 1
	return nil
}

func inspectAudioDuration(file *os.File, mediaType string) (time.Duration, error) {
	info, err := file.Stat()
	if err != nil || info.Size() <= 0 || info.Size() > maximumInspectedAudioBytes {
		return 0, errInvalidAudioContainer
	}
	data, err := io.ReadAll(io.NewSectionReader(file, 0, info.Size()))
	if err != nil {
		return 0, err
	}
	var duration time.Duration
	switch mediaType {
	case "audio/wav":
		duration, err = wavDuration(data)
	case "audio/ogg":
		duration, err = oggDuration(data)
	case "audio/webm":
		duration, err = webmDuration(data)
	default:
		err = errInvalidAudioContainer
	}
	if err != nil || duration <= 0 || duration > maximumInspectedAudioDuration {
		return 0, errInvalidAudioContainer
	}
	return duration, nil
}

func voiceDurationsMatch(declared, actual time.Duration) bool {
	difference := declared - actual
	if difference < 0 {
		difference = -difference
	}
	tolerance := 2 * time.Second
	if relative := actual / 10; relative > tolerance {
		tolerance = relative
	}
	return difference <= tolerance
}

func durationFromRatio(units, unitsPerSecond uint64) (time.Duration, error) {
	if units == 0 || unitsPerSecond == 0 {
		return 0, errInvalidAudioContainer
	}
	seconds := float64(units) / float64(unitsPerSecond)
	return durationFromSeconds(seconds)
}

func durationFromSeconds(seconds float64) (time.Duration, error) {
	if seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds > maximumInspectedAudioDuration.Seconds() {
		return 0, errInvalidAudioContainer
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func checkedAudioAdd(left, right uint64) (uint64, error) {
	if left > math.MaxUint64-right {
		return 0, errInvalidAudioContainer
	}
	return left + right, nil
}

func checkedAudioMultiply(left, right uint64) (uint64, error) {
	if left != 0 && right > math.MaxUint64/left {
		return 0, errInvalidAudioContainer
	}
	return left * right, nil
}

func checkedAudioInt(value uint64) (int, error) {
	if value > math.MaxInt {
		return 0, errInvalidAudioContainer
	}
	//nolint:gosec // The platform-int upper bound is checked immediately above.
	return int(value), nil
}

func wavDuration(data []byte) (time.Duration, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return 0, errInvalidAudioContainer
	}
	declaredSize := uint64(binary.LittleEndian.Uint32(data[4:8])) + 8
	if declaredSize != uint64(len(data)) || declaredSize < 12 {
		return 0, errInvalidAudioContainer
	}
	items := 0
	var sampleRate uint32
	var blockAlign uint16
	var audioBytes uint64
	foundFormat := false
	for offset := uint64(12); offset < declaredSize; {
		if err := consumeAudioContainerItem(&items); err != nil {
			return 0, err
		}
		if offset+8 > declaredSize {
			return 0, errInvalidAudioContainer
		}
		size := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		start := offset + 8
		end := start + size
		if end < start || end > declaredSize {
			return 0, errInvalidAudioContainer
		}
		switch string(data[offset : offset+4]) {
		case "fmt ":
			if foundFormat || size < 16 {
				return 0, errInvalidAudioContainer
			}
			foundFormat = true
			format := binary.LittleEndian.Uint16(data[start : start+2])
			channels := binary.LittleEndian.Uint16(data[start+2 : start+4])
			sampleRate = binary.LittleEndian.Uint32(data[start+4 : start+8])
			blockAlign = binary.LittleEndian.Uint16(data[start+12 : start+14])
			bitsPerSample := binary.LittleEndian.Uint16(data[start+14 : start+16])
			expectedAlign := uint32(channels) * uint32(bitsPerSample+7) / 8
			if (format != 1 && format != 3 && format != 0xfffe) || channels == 0 || sampleRate == 0 || bitsPerSample == 0 || expectedAlign == 0 || uint32(blockAlign) != expectedAlign {
				return 0, errInvalidAudioContainer
			}
		case "data":
			if !foundFormat {
				return 0, errInvalidAudioContainer
			}
			audioBytes += size
		}
		next := end + size%2
		if next > declaredSize {
			return 0, errInvalidAudioContainer
		}
		offset = next
	}
	return durationFromRatio(audioBytes, uint64(blockAlign)*uint64(sampleRate))
}

type oggCodec int

const (
	oggCodecUnknown oggCodec = iota
	oggCodecOpus
)

func oggDuration(data []byte) (time.Duration, error) {
	var selectedSerial uint32
	var selected bool
	var codec oggCodec
	var sampleRate uint64
	var preSkip uint64
	var finalGranule uint64
	var packetSamples uint64
	var packet []byte
	var seenTags bool
	var seenAudio bool
	var expectedSequence uint32
	var sawEnd bool
	items := 0
	for offset := 0; offset < len(data); {
		if err := consumeAudioContainerItem(&items); err != nil {
			return 0, err
		}
		if offset+27 > len(data) || string(data[offset:offset+4]) != "OggS" || data[offset+4] != 0 {
			return 0, errInvalidAudioContainer
		}
		segments := int(data[offset+26])
		if offset+27+segments > len(data) {
			return 0, errInvalidAudioContainer
		}
		payloadSize := 0
		for _, size := range data[offset+27 : offset+27+segments] {
			payloadSize += int(size)
		}
		pageEnd := offset + 27 + segments + payloadSize
		if pageEnd > len(data) || !validOggChecksum(data[offset:pageEnd]) {
			return 0, errInvalidAudioContainer
		}
		serial := binary.LittleEndian.Uint32(data[offset+14 : offset+18])
		sequence := binary.LittleEndian.Uint32(data[offset+18 : offset+22])
		headerType := data[offset+5]
		if !selected {
			if headerType&2 == 0 || sequence != 0 {
				return 0, errInvalidAudioContainer
			}
			selectedSerial = serial
			selected = true
		} else if serial != selectedSerial || headerType&2 != 0 || sequence != expectedSequence || sawEnd {
			return 0, errInvalidAudioContainer
		}
		expectedSequence = sequence + 1
		payloadOffset := offset + 27 + segments
		for _, lace := range data[offset+27 : offset+27+segments] {
			if err := consumeAudioContainerItem(&items); err != nil {
				return 0, err
			}
			next := payloadOffset + int(lace)
			packet = append(packet, data[payloadOffset:next]...)
			payloadOffset = next
			if lace == 255 {
				continue
			}
			switch codec {
			case oggCodecUnknown:
				switch {
				case len(packet) >= 19 && string(packet[:8]) == "OpusHead":
					codec = oggCodecOpus
					sampleRate = 48000
					preSkip = uint64(binary.LittleEndian.Uint16(packet[10:12]))
				default:
					return 0, errInvalidAudioContainer
				}
			case oggCodecOpus:
				if bytes.HasPrefix(packet, []byte("OpusTags")) {
					if seenTags || seenAudio {
						return 0, errInvalidAudioContainer
					}
					seenTags = true
				} else {
					if !seenTags {
						return 0, errInvalidAudioContainer
					}
					samples, err := opusPacketSamples(packet)
					if err != nil || packetSamples > math.MaxUint64-samples {
						return 0, errInvalidAudioContainer
					}
					packetSamples += samples
					seenAudio = true
				}
			}
			packet = packet[:0]
		}
		granule := binary.LittleEndian.Uint64(data[offset+6 : offset+14])
		if granule != math.MaxUint64 {
			finalGranule = granule
		}
		sawEnd = headerType&4 != 0
		offset = pageEnd
	}
	if !selected || codec == oggCodecUnknown || !seenTags || !seenAudio || !sawEnd || len(packet) != 0 || finalGranule <= preSkip || sampleRate == 0 {
		return 0, errInvalidAudioContainer
	}
	samples := finalGranule - preSkip
	if packetSamples <= preSkip {
		return 0, errInvalidAudioContainer
	}
	packetSamples -= preSkip
	if packetSamples > samples {
		samples = packetSamples
	}
	return durationFromRatio(samples, sampleRate)
}

func validOggChecksum(page []byte) bool {
	if len(page) < 27 {
		return false
	}
	want := binary.LittleEndian.Uint32(page[22:26])
	return calculateOggChecksum(page) == want
}

var oggCRCTable = func() [256]uint32 {
	var table [256]uint32
	for index := range table {
		checksum := uint32(index) << 24
		for range 8 {
			if checksum&0x80000000 != 0 {
				checksum = checksum<<1 ^ 0x04c11db7
			} else {
				checksum <<= 1
			}
		}
		table[index] = checksum
	}
	return table
}()

func calculateOggChecksum(page []byte) uint32 {
	var checksum uint32
	for index, value := range page {
		if index >= 22 && index < 26 {
			value = 0
		}
		checksum = checksum<<8 ^ oggCRCTable[byte(checksum>>24)^value]
	}
	return checksum
}

func opusPacketSamples(packet []byte) (uint64, error) {
	if len(packet) == 0 {
		return 0, errInvalidAudioContainer
	}
	configuration := packet[0] >> 3
	var samplesPerFrame uint64
	switch {
	case configuration < 12:
		samplesPerFrame = []uint64{480, 960, 1920, 2880}[configuration%4]
	case configuration < 16:
		samplesPerFrame = []uint64{480, 960}[configuration%2]
	default:
		samplesPerFrame = []uint64{120, 240, 480, 960}[configuration%4]
	}
	frames := uint64(1)
	switch packet[0] & 3 {
	case 1, 2:
		frames = 2
	case 3:
		if len(packet) < 2 {
			return 0, errInvalidAudioContainer
		}
		frames = uint64(packet[1] & 0x3f)
	}
	if frames == 0 || samplesPerFrame*frames > 5760 {
		return 0, errInvalidAudioContainer
	}
	return samplesPerFrame * frames, nil
}

type ebmlElement struct {
	id         uint64
	dataOffset int
	end        int
}

func readEBMLElement(data []byte, offset, limit int, allowUnknown bool, items *int) (ebmlElement, int, error) {
	if offset < 0 || limit > len(data) || offset >= limit {
		return ebmlElement{}, 0, errInvalidAudioContainer
	}
	if err := consumeAudioContainerItem(items); err != nil {
		return ebmlElement{}, 0, err
	}
	id, idWidth, _, err := readEBMLVariable(data, offset, 4, false)
	if err != nil || offset+idWidth >= limit {
		return ebmlElement{}, 0, errInvalidAudioContainer
	}
	size, sizeWidth, unknown, err := readEBMLVariable(data, offset+idWidth, 8, true)
	if err != nil || offset+idWidth+sizeWidth > limit || (unknown && !allowUnknown) {
		return ebmlElement{}, 0, errInvalidAudioContainer
	}
	start := offset + idWidth + sizeWidth
	end := limit
	if !unknown {
		sizeInt, sizeErr := checkedAudioInt(size)
		if sizeErr != nil || sizeInt > limit-start {
			return ebmlElement{}, 0, errInvalidAudioContainer
		}
		end = start + sizeInt
	}
	return ebmlElement{id: id, dataOffset: start, end: end}, end, nil
}

func readEBMLVariable(data []byte, offset, maximumWidth int, removeMarker bool) (uint64, int, bool, error) {
	if offset >= len(data) || data[offset] == 0 {
		return 0, 0, false, errInvalidAudioContainer
	}
	width := 1
	marker := byte(0x80)
	for width <= maximumWidth && data[offset]&marker == 0 {
		width++
		marker >>= 1
	}
	if marker == 0 || width > maximumWidth || offset+width > len(data) {
		return 0, 0, false, errInvalidAudioContainer
	}
	value := uint64(data[offset])
	if removeMarker {
		value = uint64(data[offset] & (marker - 1))
	}
	for index := 1; index < width; index++ {
		value = value<<8 | uint64(data[offset+index])
	}
	unknown := removeMarker && value == uint64(1)<<(7*width)-1
	return value, width, unknown, nil
}

func ebmlUint(data []byte, element ebmlElement) (uint64, error) {
	length := element.end - element.dataOffset
	if length < 1 || length > 8 {
		return 0, errInvalidAudioContainer
	}
	var value uint64
	for _, item := range data[element.dataOffset:element.end] {
		value = value<<8 | uint64(item)
	}
	return value, nil
}

func ebmlFloat(data []byte, element ebmlElement) (float64, error) {
	switch element.end - element.dataOffset {
	case 4:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(data[element.dataOffset:element.end]))), nil
	case 8:
		return math.Float64frombits(binary.BigEndian.Uint64(data[element.dataOffset:element.end])), nil
	default:
		return 0, errInvalidAudioContainer
	}
}

type webmTrack struct {
	number          uint64
	codec           string
	defaultDuration uint64
	sampleRate      float64
}

func webmDuration(data []byte) (time.Duration, error) {
	items := 0
	var segment ebmlElement
	foundHeader := false
	for offset := 0; offset < len(data); {
		element, next, err := readEBMLElement(data, offset, len(data), isWebMSegment(data, offset), &items)
		if err != nil {
			return 0, err
		}
		switch element.id {
		case 0x1a45dfa3:
			if foundHeader {
				return 0, errInvalidAudioContainer
			}
			foundHeader = true
		case 0x18538067:
			if segment.id != 0 {
				return 0, errInvalidAudioContainer
			}
			segment = element
		}
		offset = next
	}
	if !foundHeader || segment.id == 0 {
		return 0, errInvalidAudioContainer
	}
	timecodeScale := uint64(1000000)
	var declaredTicks float64
	var audioTrack webmTrack
	foundInfo := false
	foundTracks := false
	foundCluster := false
	for offset := segment.dataOffset; offset < segment.end; {
		element, next, err := readWebMSegmentElement(data, offset, segment.end, &items)
		if err != nil {
			return 0, err
		}
		switch element.id {
		case 0x1549a966:
			if foundInfo {
				return 0, errInvalidAudioContainer
			}
			foundInfo = true
			foundScale := false
			foundDuration := false
			for childOffset := element.dataOffset; childOffset < element.end; {
				child, childNext, childErr := readEBMLElement(data, childOffset, element.end, false, &items)
				if childErr != nil {
					return 0, childErr
				}
				switch child.id {
				case 0x2ad7b1:
					if foundScale {
						return 0, errInvalidAudioContainer
					}
					foundScale = true
					timecodeScale, childErr = ebmlUint(data, child)
				case 0x4489:
					if foundDuration {
						return 0, errInvalidAudioContainer
					}
					foundDuration = true
					declaredTicks, childErr = ebmlFloat(data, child)
				}
				if childErr != nil {
					return 0, childErr
				}
				childOffset = childNext
			}
		case 0x1654ae6b:
			if foundTracks {
				return 0, errInvalidAudioContainer
			}
			foundTracks = true
			track, trackErr := findWebMAudioTrack(data, element, &items)
			if trackErr != nil {
				return 0, trackErr
			}
			if track.number != 0 {
				if audioTrack.number != 0 {
					return 0, errInvalidAudioContainer
				}
				audioTrack = track
			}
		case 0x1f43b675:
			foundCluster = true
		}
		offset = next
	}
	if !foundInfo || !foundTracks || timecodeScale == 0 || audioTrack.number == 0 || !foundCluster {
		return 0, errInvalidAudioContainer
	}
	if audioTrack.codec == "A_AAC" && (audioTrack.sampleRate <= 0 || math.IsNaN(audioTrack.sampleRate) || math.IsInf(audioTrack.sampleRate, 0)) {
		return 0, errInvalidAudioContainer
	}
	var maximumNanoseconds uint64
	var encodedNanoseconds uint64
	foundBlock := false
	for offset := segment.dataOffset; offset < segment.end; {
		element, next, err := readWebMSegmentElement(data, offset, segment.end, &items)
		if err != nil {
			return 0, err
		}
		if element.id == 0x1f43b675 {
			clusterMaximum, clusterEncoded, clusterFound, inspectErr := inspectWebMCluster(
				data,
				element,
				audioTrack,
				timecodeScale,
				&items,
			)
			if inspectErr != nil {
				return 0, inspectErr
			}
			if clusterMaximum > maximumNanoseconds {
				maximumNanoseconds = clusterMaximum
			}
			encodedNanoseconds, err = checkedAudioAdd(encodedNanoseconds, clusterEncoded)
			if err != nil || encodedNanoseconds > uint64(maximumInspectedAudioDuration) {
				return 0, errInvalidAudioContainer
			}
			foundBlock = foundBlock || clusterFound
		}
		offset = next
	}
	if !foundBlock {
		return 0, errInvalidAudioContainer
	}
	if declaredTicks > 0 {
		declaredNanoseconds := declaredTicks * float64(timecodeScale)
		if math.IsNaN(declaredNanoseconds) || math.IsInf(declaredNanoseconds, 0) || declaredNanoseconds > float64(math.MaxInt64) {
			return 0, errInvalidAudioContainer
		}
		if uint64(declaredNanoseconds) > maximumNanoseconds {
			maximumNanoseconds = uint64(declaredNanoseconds)
		}
	}
	if encodedNanoseconds > maximumNanoseconds {
		maximumNanoseconds = encodedNanoseconds
	}
	return durationFromRatio(maximumNanoseconds, uint64(time.Second))
}

func isWebMSegment(data []byte, offset int) bool {
	id, _, _, err := readEBMLVariable(data, offset, 4, false)
	return err == nil && id == 0x18538067
}

func readWebMSegmentElement(data []byte, offset, limit int, items *int) (ebmlElement, int, error) {
	id, idWidth, _, err := readEBMLVariable(data, offset, 4, false)
	if err != nil || offset+idWidth >= limit {
		return ebmlElement{}, 0, errInvalidAudioContainer
	}
	_, sizeWidth, unknown, err := readEBMLVariable(data, offset+idWidth, 8, true)
	if err != nil || offset+idWidth+sizeWidth > limit {
		return ebmlElement{}, 0, errInvalidAudioContainer
	}
	if !unknown {
		return readEBMLElement(data, offset, limit, false, items)
	}
	if id != 0x1f43b675 {
		return ebmlElement{}, 0, errInvalidAudioContainer
	}
	if err = consumeAudioContainerItem(items); err != nil {
		return ebmlElement{}, 0, err
	}
	start := offset + idWidth + sizeWidth
	for childOffset := start; childOffset < limit; {
		childID, childIDWidth, _, childErr := readEBMLVariable(data, childOffset, 4, false)
		if childErr != nil || childOffset+childIDWidth > limit {
			return ebmlElement{}, 0, errInvalidAudioContainer
		}
		if isWebMSegmentLevelOne(childID) {
			return ebmlElement{id: id, dataOffset: start, end: childOffset}, childOffset, nil
		}
		_, next, childErr := readEBMLElement(data, childOffset, limit, false, items)
		if childErr != nil {
			return ebmlElement{}, 0, childErr
		}
		childOffset = next
	}
	return ebmlElement{id: id, dataOffset: start, end: limit}, limit, nil
}

func isWebMSegmentLevelOne(id uint64) bool {
	switch id {
	case 0x114d9b74, 0x1549a966, 0x1f43b675, 0x1654ae6b,
		0x1c53bb6b, 0x1941a469, 0x1043a770, 0x1254c367:
		return true
	default:
		return false
	}
}

func findWebMAudioTrack(data []byte, tracks ebmlElement, items *int) (webmTrack, error) {
	var audioTrack webmTrack
	for offset := tracks.dataOffset; offset < tracks.end; {
		entry, next, err := readEBMLElement(data, offset, tracks.end, false, items)
		if err != nil {
			return webmTrack{}, err
		}
		if entry.id == 0xae {
			track, audio, entryErr := parseWebMTrackEntry(data, entry, items)
			if entryErr != nil {
				return webmTrack{}, entryErr
			}
			if audio {
				if track.codec != "A_OPUS" && track.codec != "A_AAC" {
					return webmTrack{}, errInvalidAudioContainer
				}
				if audioTrack.number != 0 {
					return webmTrack{}, errInvalidAudioContainer
				}
				audioTrack = track
			}
		}
		offset = next
	}
	return audioTrack, nil
}

func parseWebMTrackEntry(data []byte, entry ebmlElement, items *int) (webmTrack, bool, error) {
	var track webmTrack
	var trackType uint64
	foundNumber := false
	foundType := false
	foundCodec := false
	foundDefaultDuration := false
	foundAudio := false
	for offset := entry.dataOffset; offset < entry.end; {
		child, next, err := readEBMLElement(data, offset, entry.end, false, items)
		if err != nil {
			return webmTrack{}, false, err
		}
		switch child.id {
		case 0xd7:
			if foundNumber {
				return webmTrack{}, false, errInvalidAudioContainer
			}
			foundNumber = true
			track.number, err = ebmlUint(data, child)
		case 0x83:
			if foundType {
				return webmTrack{}, false, errInvalidAudioContainer
			}
			foundType = true
			trackType, err = ebmlUint(data, child)
		case 0x86:
			if foundCodec {
				return webmTrack{}, false, errInvalidAudioContainer
			}
			foundCodec = true
			track.codec = string(data[child.dataOffset:child.end])
		case 0x23e383:
			if foundDefaultDuration {
				return webmTrack{}, false, errInvalidAudioContainer
			}
			foundDefaultDuration = true
			track.defaultDuration, err = ebmlUint(data, child)
		case 0xe1:
			if foundAudio {
				return webmTrack{}, false, errInvalidAudioContainer
			}
			foundAudio = true
			foundSampleRate := false
			for audioOffset := child.dataOffset; audioOffset < child.end; {
				audioChild, audioNext, audioErr := readEBMLElement(data, audioOffset, child.end, false, items)
				if audioErr != nil {
					return webmTrack{}, false, audioErr
				}
				if audioChild.id == 0xb5 {
					if foundSampleRate {
						return webmTrack{}, false, errInvalidAudioContainer
					}
					foundSampleRate = true
					track.sampleRate, audioErr = ebmlFloat(data, audioChild)
					if audioErr != nil {
						return webmTrack{}, false, audioErr
					}
				}
				audioOffset = audioNext
			}
		}
		if err != nil {
			return webmTrack{}, false, err
		}
		offset = next
	}
	return track, trackType == 2 && track.number != 0 && track.codec != "", nil
}

func inspectWebMCluster(
	data []byte,
	cluster ebmlElement,
	track webmTrack,
	scale uint64,
	items *int,
) (uint64, uint64, bool, error) {
	var clusterTime uint64
	foundTime := false
	for offset := cluster.dataOffset; offset < cluster.end; {
		child, next, err := readEBMLElement(data, offset, cluster.end, false, items)
		if err != nil {
			return 0, 0, false, err
		}
		if child.id == 0xe7 {
			if foundTime {
				return 0, 0, false, errInvalidAudioContainer
			}
			clusterTime, err = ebmlUint(data, child)
			if err != nil {
				return 0, 0, false, err
			}
			foundTime = true
		}
		offset = next
	}
	if !foundTime {
		return 0, 0, false, errInvalidAudioContainer
	}

	var maximum uint64
	var encodedNanoseconds uint64
	foundBlock := false
	for offset := cluster.dataOffset; offset < cluster.end; {
		child, next, err := readEBMLElement(data, offset, cluster.end, false, items)
		if err != nil {
			return 0, 0, false, err
		}
		if child.id == 0xa3 || child.id == 0xa0 {
			end, encoded, matches, inspectErr := inspectWebMBlockElement(
				data,
				child,
				track,
				clusterTime,
				scale,
				items,
			)
			if inspectErr != nil {
				return 0, 0, false, inspectErr
			}
			if matches {
				if end > maximum {
					maximum = end
				}
				encodedNanoseconds, err = checkedAudioAdd(encodedNanoseconds, encoded)
				if err != nil || encodedNanoseconds > uint64(maximumInspectedAudioDuration) {
					return 0, 0, false, errInvalidAudioContainer
				}
				foundBlock = true
			}
		}
		offset = next
	}
	return maximum, encodedNanoseconds, foundBlock, nil
}

func inspectWebMBlockElement(
	data []byte,
	blockElement ebmlElement,
	track webmTrack,
	clusterTime uint64,
	scale uint64,
	items *int,
) (uint64, uint64, bool, error) {
	block := blockElement
	var explicitDuration uint64
	if blockElement.id == 0xa0 {
		block = ebmlElement{}
		for offset := blockElement.dataOffset; offset < blockElement.end; {
			child, next, err := readEBMLElement(data, offset, blockElement.end, false, items)
			if err != nil {
				return 0, 0, false, err
			}
			switch child.id {
			case 0xa1:
				if block.id != 0 {
					return 0, 0, false, errInvalidAudioContainer
				}
				block = child
			case 0x9b:
				explicitDuration, err = ebmlUint(data, child)
			}
			if err != nil {
				return 0, 0, false, err
			}
			offset = next
		}
		if block.id == 0 {
			return 0, 0, false, errInvalidAudioContainer
		}
	}
	blockTrack, relativeMagnitude, relativeNegative, frames, err := parseWebMBlock(data[block.dataOffset:block.end])
	if err != nil {
		return 0, 0, false, err
	}
	if blockTrack != track.number {
		return 0, 0, false, nil
	}
	startTicks := clusterTime
	if relativeNegative {
		if relativeMagnitude > startTicks {
			return 0, 0, false, errInvalidAudioContainer
		}
		startTicks -= relativeMagnitude
	} else {
		startTicks, err = checkedAudioAdd(startTicks, relativeMagnitude)
		if err != nil {
			return 0, 0, false, errInvalidAudioContainer
		}
	}
	startNanoseconds, err := checkedAudioMultiply(startTicks, scale)
	if err != nil || startNanoseconds > uint64(maximumInspectedAudioDuration) {
		return 0, 0, false, errInvalidAudioContainer
	}
	var encodedNanoseconds uint64
	switch track.codec {
	case "A_OPUS":
		var blockSamples uint64
		for _, frame := range frames {
			samples, sampleErr := opusPacketSamples(frame)
			if sampleErr != nil {
				return 0, 0, false, sampleErr
			}
			blockSamples, err = checkedAudioAdd(blockSamples, samples)
			if err != nil {
				return 0, 0, false, err
			}
		}
		encodedNanoseconds = blockSamples * uint64(time.Second) / 48000
	case "A_AAC":
		encoded := float64(len(frames)*1024) / track.sampleRate * float64(time.Second)
		if encoded <= 0 || math.IsNaN(encoded) || math.IsInf(encoded, 0) || encoded > float64(math.MaxUint64) {
			return 0, 0, false, errInvalidAudioContainer
		}
		encodedNanoseconds = uint64(encoded)
	}
	end, err := checkedAudioAdd(startNanoseconds, encodedNanoseconds)
	if err != nil {
		return 0, 0, false, err
	}
	if explicitDuration > 0 {
		duration, multiplyErr := checkedAudioMultiply(explicitDuration, scale)
		if multiplyErr != nil {
			return 0, 0, false, multiplyErr
		}
		end, err = checkedAudioAdd(startNanoseconds, duration)
	} else if track.defaultDuration > 0 {
		duration, multiplyErr := checkedAudioMultiply(uint64(len(frames)), track.defaultDuration)
		if multiplyErr != nil {
			return 0, 0, false, multiplyErr
		}
		end, err = checkedAudioAdd(startNanoseconds, duration)
	}
	if err != nil || end > uint64(maximumInspectedAudioDuration) {
		return 0, 0, false, errInvalidAudioContainer
	}
	return end, encodedNanoseconds, true, nil
}

func parseWebMBlock(block []byte) (uint64, uint64, bool, [][]byte, error) {
	track, width, _, err := readEBMLVariable(block, 0, 8, true)
	if err != nil || len(block) < width+3 {
		return 0, 0, false, nil, errInvalidAudioContainer
	}
	relative := binary.BigEndian.Uint16(block[width : width+2])
	relativeNegative := relative&0x8000 != 0
	relativeMagnitude := uint64(relative)
	if relativeNegative {
		relativeMagnitude = uint64(^relative + 1)
	}
	flags := block[width+2]
	payload := block[width+3:]
	frames, err := splitWebMLacing(payload, (flags>>1)&3)
	return track, relativeMagnitude, relativeNegative, frames, err
}

func splitWebMLacing(payload []byte, lacing byte) ([][]byte, error) {
	if lacing == 0 {
		if len(payload) == 0 {
			return nil, errInvalidAudioContainer
		}
		return [][]byte{payload}, nil
	}
	if len(payload) < 2 {
		return nil, errInvalidAudioContainer
	}
	count := int(payload[0]) + 1
	payload = payload[1:]
	sizes := make([]int, count)
	switch lacing {
	case 1:
		for index := 0; index < count-1; index++ {
			for {
				if len(payload) == 0 {
					return nil, errInvalidAudioContainer
				}
				value := int(payload[0])
				payload = payload[1:]
				sizes[index] += value
				if value != 255 {
					break
				}
			}
		}
	case 2:
		if len(payload)%count != 0 {
			return nil, errInvalidAudioContainer
		}
		for index := range sizes {
			sizes[index] = len(payload) / count
		}
	case 3:
		first, width, _, err := readEBMLVariable(payload, 0, 8, true)
		firstSize, firstErr := checkedAudioInt(first)
		if err != nil || firstErr != nil {
			return nil, errInvalidAudioContainer
		}
		sizes[0] = firstSize
		payload = payload[width:]
		for index := 1; index < count-1; index++ {
			encoded, encodedWidth, _, itemErr := readEBMLVariable(payload, 0, 8, true)
			if itemErr != nil {
				return nil, errInvalidAudioContainer
			}
			bias := uint64(1)<<(7*encodedWidth-1) - 1
			previous := sizes[index-1]
			if encoded >= bias {
				increase, increaseErr := checkedAudioInt(encoded - bias)
				if increaseErr != nil || increase > math.MaxInt-previous {
					return nil, errInvalidAudioContainer
				}
				sizes[index] = previous + increase
			} else {
				decrease, decreaseErr := checkedAudioInt(bias - encoded)
				if decreaseErr != nil || decrease > previous {
					return nil, errInvalidAudioContainer
				}
				sizes[index] = previous - decrease
			}
			payload = payload[encodedWidth:]
		}
	}
	known := 0
	for index := 0; index < count-1; index++ {
		if sizes[index] < 0 || sizes[index] > len(payload)-known {
			return nil, errInvalidAudioContainer
		}
		known += sizes[index]
	}
	if lacing != 2 {
		sizes[count-1] = len(payload) - known
	}
	frames := make([][]byte, 0, count)
	offset := 0
	for _, size := range sizes {
		if size <= 0 || offset+size > len(payload) {
			return nil, errInvalidAudioContainer
		}
		frames = append(frames, payload[offset:offset+size])
		offset += size
	}
	if offset != len(payload) {
		return nil, errInvalidAudioContainer
	}
	return frames, nil
}

type mp4Box struct {
	typ        string
	dataOffset int
	end        int
}

func mp4Boxes(data []byte, start, end int, items *int) ([]mp4Box, error) {
	if start < 0 || end > len(data) || start > end {
		return nil, errInvalidAudioContainer
	}
	var boxes []mp4Box
	for offset := start; offset < end; {
		if err := consumeAudioContainerItem(items); err != nil {
			return nil, err
		}
		if offset+8 > end {
			return nil, errInvalidAudioContainer
		}
		rawSize := binary.BigEndian.Uint32(data[offset : offset+4])
		var size int
		header := 8
		var sizeErr error
		switch rawSize {
		case 1:
			if offset+16 > end {
				return nil, errInvalidAudioContainer
			}
			size, sizeErr = checkedAudioInt(binary.BigEndian.Uint64(data[offset+8 : offset+16]))
			header = 16
		case 0:
			size = end - offset
		default:
			size, sizeErr = checkedAudioInt(uint64(rawSize))
		}
		if sizeErr != nil || size < header || size > end-offset {
			return nil, errInvalidAudioContainer
		}
		boxEnd := offset + size
		boxes = append(boxes, mp4Box{typ: string(data[offset+4 : offset+8]), dataOffset: offset + header, end: boxEnd})
		offset = boxEnd
	}
	return boxes, nil
}

func findUniqueMP4Box(boxes []mp4Box, boxType string) (mp4Box, bool, error) {
	var found mp4Box
	for _, box := range boxes {
		if box.typ != boxType {
			continue
		}
		if found.typ != "" {
			return mp4Box{}, false, errInvalidAudioContainer
		}
		found = box
	}
	return found, found.typ != "", nil
}

type mp4AudioTrack struct {
	id        uint32
	timeScale uint32
	units     uint64
}

func mp4Duration(data []byte) (time.Duration, error) {
	items := 0
	boxes, err := mp4Boxes(data, 0, len(data), &items)
	if err != nil {
		return 0, err
	}
	var moov mp4Box
	hasFileType := false
	hasMedia := false
	for _, box := range boxes {
		switch box.typ {
		case "ftyp":
			if hasFileType {
				return 0, errInvalidAudioContainer
			}
			hasFileType = true
		case "moov":
			if moov.typ != "" {
				return 0, errInvalidAudioContainer
			}
			moov = box
		case "mdat":
			hasMedia = hasMedia || box.end > box.dataOffset
		}
	}
	if !hasFileType || moov.typ == "" || !hasMedia {
		return 0, errInvalidAudioContainer
	}
	children, err := mp4Boxes(data, moov.dataOffset, moov.end, &items)
	if err != nil {
		return 0, err
	}
	var track mp4AudioTrack
	for _, child := range children {
		if child.typ != "trak" {
			continue
		}
		candidate, audio, trackErr := parseMP4Track(data, child, &items)
		if trackErr != nil {
			return 0, trackErr
		}
		if audio {
			if track.id != 0 {
				return 0, errInvalidAudioContainer
			}
			track = candidate
		}
	}
	if track.id == 0 || track.timeScale == 0 {
		return 0, errInvalidAudioContainer
	}
	maximumUnits, err := checkedAudioMultiply(
		uint64(track.timeScale),
		uint64(maximumInspectedAudioDuration/time.Second),
	)
	if err != nil || track.units > maximumUnits {
		return 0, errInvalidAudioContainer
	}
	fragmentUnits, err := mp4FragmentDuration(data, boxes, track.id, maximumUnits, &items)
	if err != nil {
		return 0, err
	}
	if fragmentUnits > track.units {
		track.units = fragmentUnits
	}
	return durationFromRatio(track.units, uint64(track.timeScale))
}

func parseMP4Track(data []byte, trackBox mp4Box, items *int) (mp4AudioTrack, bool, error) {
	children, err := mp4Boxes(data, trackBox.dataOffset, trackBox.end, items)
	if err != nil {
		return mp4AudioTrack{}, false, err
	}
	tkhd, hasHeader, err := findUniqueMP4Box(children, "tkhd")
	if err != nil {
		return mp4AudioTrack{}, false, err
	}
	mdia, hasMedia, err := findUniqueMP4Box(children, "mdia")
	if err != nil || !hasHeader || !hasMedia {
		return mp4AudioTrack{}, false, errInvalidAudioContainer
	}
	trackID, err := mp4TrackID(data[tkhd.dataOffset:tkhd.end])
	if err != nil {
		return mp4AudioTrack{}, false, err
	}
	mediaChildren, err := mp4Boxes(data, mdia.dataOffset, mdia.end, items)
	if err != nil {
		return mp4AudioTrack{}, false, err
	}
	handler, hasHandler, err := findUniqueMP4Box(mediaChildren, "hdlr")
	if err != nil {
		return mp4AudioTrack{}, false, err
	}
	if !hasHandler || handler.end-handler.dataOffset < 12 {
		return mp4AudioTrack{}, false, errInvalidAudioContainer
	}
	if string(data[handler.dataOffset+8:handler.dataOffset+12]) != "soun" {
		return mp4AudioTrack{}, false, nil
	}
	mdhd, hasMediaHeader, err := findUniqueMP4Box(mediaChildren, "mdhd")
	if err != nil {
		return mp4AudioTrack{}, false, err
	}
	if !hasMediaHeader {
		return mp4AudioTrack{}, false, errInvalidAudioContainer
	}
	timeScale, headerUnits, err := mp4MediaDuration(data[mdhd.dataOffset:mdhd.end])
	if err != nil {
		return mp4AudioTrack{}, false, err
	}
	units := headerUnits
	minf, found, err := findUniqueMP4Box(mediaChildren, "minf")
	if err != nil {
		return mp4AudioTrack{}, false, err
	}
	if found {
		minfChildren, childErr := mp4Boxes(data, minf.dataOffset, minf.end, items)
		if childErr != nil {
			return mp4AudioTrack{}, false, childErr
		}
		stbl, tableFound, uniqueErr := findUniqueMP4Box(minfChildren, "stbl")
		if uniqueErr != nil {
			return mp4AudioTrack{}, false, uniqueErr
		}
		if tableFound {
			tableChildren, tableErr := mp4Boxes(data, stbl.dataOffset, stbl.end, items)
			if tableErr != nil {
				return mp4AudioTrack{}, false, tableErr
			}
			stts, timingFound, timingFindErr := findUniqueMP4Box(tableChildren, "stts")
			if timingFindErr != nil {
				return mp4AudioTrack{}, false, timingFindErr
			}
			if timingFound {
				tableUnits, timingErr := mp4TimingDuration(data[stts.dataOffset:stts.end], items)
				if timingErr != nil {
					return mp4AudioTrack{}, false, timingErr
				}
				if tableUnits > units {
					units = tableUnits
				}
			}
		}
	}
	return mp4AudioTrack{id: trackID, timeScale: timeScale, units: units}, true, nil
}

func mp4TrackID(data []byte) (uint32, error) {
	if len(data) < 16 {
		return 0, errInvalidAudioContainer
	}
	if data[0] == 1 {
		if len(data) < 24 {
			return 0, errInvalidAudioContainer
		}
		return binary.BigEndian.Uint32(data[20:24]), nil
	}
	return binary.BigEndian.Uint32(data[12:16]), nil
}

func mp4MediaDuration(data []byte) (uint32, uint64, error) {
	if len(data) < 24 {
		return 0, 0, errInvalidAudioContainer
	}
	if data[0] == 1 {
		if len(data) < 32 {
			return 0, 0, errInvalidAudioContainer
		}
		return binary.BigEndian.Uint32(data[20:24]), binary.BigEndian.Uint64(data[24:32]), nil
	}
	return binary.BigEndian.Uint32(data[12:16]), uint64(binary.BigEndian.Uint32(data[16:20])), nil
}

func mp4TimingDuration(data []byte, items *int) (uint64, error) {
	if len(data) < 8 {
		return 0, errInvalidAudioContainer
	}
	count, err := checkedAudioInt(uint64(binary.BigEndian.Uint32(data[4:8])))
	if err != nil || count > maximumAudioContainerItems-*items || 8+count*8 != len(data) {
		return 0, errInvalidAudioContainer
	}
	*items += count
	var units uint64
	for index := 0; index < count; index++ {
		offset := 8 + index*8
		samples := uint64(binary.BigEndian.Uint32(data[offset : offset+4]))
		delta := uint64(binary.BigEndian.Uint32(data[offset+4 : offset+8]))
		if samples != 0 && delta > (math.MaxUint64-units)/samples {
			return 0, errInvalidAudioContainer
		}
		units += samples * delta
	}
	return units, nil
}

func mp4FragmentDuration(data []byte, boxes []mp4Box, trackID uint32, maximumUnits uint64, items *int) (uint64, error) {
	var maximum uint64
	for _, box := range boxes {
		if box.typ != "moof" {
			continue
		}
		children, err := mp4Boxes(data, box.dataOffset, box.end, items)
		if err != nil {
			return 0, err
		}
		for _, child := range children {
			if child.typ != "traf" {
				continue
			}
			end, matches, inspectErr := inspectMP4TrackFragment(data, child, trackID, maximumUnits, items)
			if inspectErr != nil {
				return 0, inspectErr
			}
			if matches && end > maximum {
				maximum = end
			}
		}
	}
	return maximum, nil
}

func inspectMP4TrackFragment(data []byte, fragment mp4Box, wantedTrack uint32, maximumUnits uint64, items *int) (uint64, bool, error) {
	children, err := mp4Boxes(data, fragment.dataOffset, fragment.end, items)
	if err != nil {
		return 0, false, err
	}
	var tfhd mp4Box
	var tfdt mp4Box
	for _, child := range children {
		switch child.typ {
		case "tfhd":
			if tfhd.typ != "" {
				return 0, false, errInvalidAudioContainer
			}
			tfhd = child
		case "tfdt":
			if tfdt.typ != "" {
				return 0, false, errInvalidAudioContainer
			}
			tfdt = child
		}
	}
	if tfhd.typ == "" || tfhd.end-tfhd.dataOffset < 8 {
		return 0, false, errInvalidAudioContainer
	}
	header := data[tfhd.dataOffset:tfhd.end]
	flags := uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])
	const allowedTFHDFlags = 0x000001 | 0x000002 | 0x000008 | 0x000010 | 0x000020 | 0x010000 | 0x020000
	if header[0] != 0 || flags & ^uint32(allowedTFHDFlags) != 0 {
		return 0, false, errInvalidAudioContainer
	}
	trackID := binary.BigEndian.Uint32(header[4:8])
	if trackID != wantedTrack {
		return 0, false, nil
	}
	if flags&0x010000 != 0 || tfdt.typ == "" {
		return 0, false, errInvalidAudioContainer
	}
	offset := 8
	if flags&0x000001 != 0 {
		offset += 8
	}
	if flags&0x000002 != 0 {
		offset += 4
	}
	var defaultDuration uint32
	if flags&0x000008 != 0 {
		if offset+4 > len(header) {
			return 0, false, errInvalidAudioContainer
		}
		defaultDuration = binary.BigEndian.Uint32(header[offset : offset+4])
		offset += 4
	}
	if flags&0x000010 != 0 {
		offset += 4
	}
	if flags&0x000020 != 0 {
		offset += 4
	}
	if offset != len(header) {
		return 0, false, errInvalidAudioContainer
	}
	timeData := data[tfdt.dataOffset:tfdt.end]
	if len(timeData) < 8 || timeData[1] != 0 || timeData[2] != 0 || timeData[3] != 0 {
		return 0, false, errInvalidAudioContainer
	}
	var base uint64
	switch timeData[0] {
	case 0:
		if len(timeData) != 8 {
			return 0, false, errInvalidAudioContainer
		}
		base = uint64(binary.BigEndian.Uint32(timeData[4:8]))
	case 1:
		if len(timeData) != 12 {
			return 0, false, errInvalidAudioContainer
		}
		base = binary.BigEndian.Uint64(timeData[4:12])
	default:
		return 0, false, errInvalidAudioContainer
	}
	if base > maximumUnits {
		return 0, false, errInvalidAudioContainer
	}
	units := base
	foundRun := false
	for _, child := range children {
		if child.typ != "trun" {
			continue
		}
		added, parseErr := mp4RunDuration(
			data[child.dataOffset:child.end],
			defaultDuration,
			maximumUnits-units,
			items,
		)
		if parseErr != nil {
			return 0, false, errInvalidAudioContainer
		}
		units += added
		foundRun = true
	}
	if !foundRun {
		return 0, false, errInvalidAudioContainer
	}
	return units, true, nil
}

func mp4RunDuration(data []byte, defaultDuration uint32, maximumUnits uint64, items *int) (uint64, error) {
	if len(data) < 8 {
		return 0, errInvalidAudioContainer
	}
	flags := uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	const allowedTRUNFlags = 0x000001 | 0x000004 | 0x000100 | 0x000200 | 0x000400 | 0x000800
	if data[0] > 1 || flags & ^uint32(allowedTRUNFlags) != 0 || flags&0x000004 != 0 && flags&0x000400 != 0 {
		return 0, errInvalidAudioContainer
	}
	countRaw := binary.BigEndian.Uint32(data[4:8])
	count, err := checkedAudioInt(uint64(countRaw))
	if err != nil || count == 0 || count > maximumAudioContainerItems-*items {
		return 0, errInvalidAudioContainer
	}
	offset := 8
	if flags&0x000001 != 0 {
		offset += 4
	}
	if flags&0x000004 != 0 {
		offset += 4
	}
	recordSize := 0
	for _, flag := range []uint32{0x000100, 0x000200, 0x000400, 0x000800} {
		if flags&flag != 0 {
			recordSize += 4
		}
	}
	if offset > len(data) || count*recordSize != len(data)-offset {
		return 0, errInvalidAudioContainer
	}
	*items += count
	if flags&0x000100 == 0 {
		units, multiplyErr := checkedAudioMultiply(uint64(countRaw), uint64(defaultDuration))
		if defaultDuration == 0 || multiplyErr != nil || units > maximumUnits {
			return 0, errInvalidAudioContainer
		}
		return units, nil
	}
	var units uint64
	for range count {
		duration := binary.BigEndian.Uint32(data[offset : offset+4])
		offset += recordSize
		var addErr error
		units, addErr = checkedAudioAdd(units, uint64(duration))
		if duration == 0 || addErr != nil || units > maximumUnits {
			return 0, errInvalidAudioContainer
		}
	}
	return units, nil
}
