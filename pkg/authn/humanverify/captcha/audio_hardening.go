package captcha

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	libcaptcha "github.com/mojocn/base64Captcha"
)

// The wave format the library writes and this file post-processes: a canonical
// 44-byte header followed by unsigned 8-bit mono PCM at 8 kHz.
const (
	audioSampleRate = 8000
	waveHeaderLen   = 44
)

// The post-processing ranges. The library already randomizes each digit's speed
// and mixes reversed digits into the background at a low level; these factors
// sit on top of that and change the whole reading, so a speech recognizer cannot
// key on one fixed pace or one clean signal.
const (
	audioSpeedMin      = 0.86
	audioSpeedMax      = 1.16
	audioNoiseMin      = 3
	audioNoiseMax      = 9
	babbleLevelMin     = 0.10
	babbleLevelMax     = 0.16
	babbleMinLengthDiv = 4
)

// hardenAudio post-processes one library audio: it changes the pace of the whole
// reading, adds a stronger noise floor than the library's own, and mixes a
// second spoken digit stream underneath the answer at a low level. The answer
// is unaffected; only the signal a recognizer sees changes.
//
// base64Captcha v1.3.8 offers no noise, speed or multi-voice setting on
// DriverAudio, so this is done here on the WAV bytes with the standard library.
// The second stream is the same voice pronouncing other digits: the library does
// not ship a second voice, so this is competing speech rather than true
// multi-voice synthesis.
func hardenAudio(body []byte, language string) ([]byte, error) {
	pcm, err := wavePCM(body)
	if err != nil {
		return nil, err
	}
	speed, err := randomFloat(audioSpeedMin, audioSpeedMax)
	if err != nil {
		return nil, err
	}
	pcm = resample(pcm, speed)
	noise, err := randomInt(audioNoiseMin, audioNoiseMax)
	if err != nil {
		return nil, err
	}
	if noiseErr := addNoise(pcm, noise); noiseErr != nil {
		return nil, noiseErr
	}
	babble, err := drawBabble(language, len(pcm))
	if err != nil {
		return nil, err
	}
	level, err := randomFloat(babbleLevelMin, babbleLevelMax)
	if err != nil {
		return nil, err
	}
	if mixErr := mixBabble(pcm, babble, level); mixErr != nil {
		return nil, mixErr
	}
	encoded, err := encodeWave(pcm)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

// wavePCM reads the PCM body of the canonical wave the library writes. Anything
// else is refused rather than misread as samples.
func wavePCM(body []byte) ([]byte, error) {
	if len(body) < waveHeaderLen ||
		string(body[0:4]) != "RIFF" ||
		string(body[8:12]) != "WAVE" ||
		string(body[12:16]) != "fmt " ||
		string(body[36:40]) != "data" {
		return nil, errors.New("captcha: the audio is not the expected RIFF/WAVE file")
	}
	if binary.LittleEndian.Uint16(body[20:22]) != 1 ||
		binary.LittleEndian.Uint16(body[22:24]) != 1 ||
		binary.LittleEndian.Uint32(body[24:28]) != audioSampleRate ||
		binary.LittleEndian.Uint16(body[34:36]) != 8 {
		return nil, errors.New("captcha: the audio is not 8-bit mono PCM at 8 kHz")
	}
	return append([]byte(nil), body[waveHeaderLen:]...), nil
}

// encodeWave writes the canonical 44-byte header the library writes, over pcm.
// The format names chunk lengths in 32 bits, so a body larger than that cannot
// be described and is refused.
func encodeWave(pcm []byte) ([]byte, error) {
	n := int64(len(pcm))
	if n < 0 || n > math.MaxUint32 {
		return nil, errors.New("captcha: the audio is longer than a wave file can describe")
	}
	if n > math.MaxUint32-(waveHeaderLen-8) {
		return nil, errors.New("captcha: the audio's RIFF chunk would not fit its 32-bit length")
	}
	sampleBytes := uint32(n)
	header := make([]byte, waveHeaderLen)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], waveHeaderLen-8+sampleBytes)
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], 1)
	binary.LittleEndian.PutUint32(header[24:28], audioSampleRate)
	binary.LittleEndian.PutUint32(header[28:32], audioSampleRate)
	binary.LittleEndian.PutUint16(header[32:34], 1)
	binary.LittleEndian.PutUint16(header[34:36], 8)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], sampleBytes)
	return append(header, pcm...), nil
}

// resample changes the pace of the reading by nearest-neighbour resampling. A
// factor above one is faster and shorter, below one slower and longer; both the
// speed and the pitch move, which is what the library's own changeSpeed does.
func resample(pcm []byte, speed float64) []byte {
	if len(pcm) == 0 {
		return nil
	}
	out := make([]byte, int(float64(len(pcm))/speed))
	for i := range out {
		source := int(float64(i) * speed)
		if source >= len(pcm) {
			source = len(pcm) - 1
		}
		out[i] = pcm[source]
	}
	return out
}

// addNoise raises the floor by a random amplitude in both directions around the
// 8-bit centre. One random buffer is drawn for the whole signal rather than one
// draw per sample.
func addNoise(pcm []byte, amplitude int) error {
	noise := make([]byte, len(pcm))
	if _, readErr := rand.Read(noise); readErr != nil {
		return readErr
	}
	span := 2*amplitude + 1
	for i := range pcm {
		delta := int(noise[i])%span - amplitude
		pcm[i] = clampSample(int(pcm[i]) + delta)
	}
	return nil
}

// drawBabble renders a second library audio of random digits in the same
// language and returns its PCM. It is mixed under the answer as competing
// speech.
func drawBabble(language string, length int) ([]byte, error) {
	if length <= 0 {
		return nil, nil
	}
	driver := libcaptcha.NewDriverAudio(audioLength, language)
	_, question, _ := driver.GenerateIdQuestionAnswer()
	item, err := driver.DrawCaptcha(question)
	if err != nil {
		return nil, fmt.Errorf("captcha: drawing the competing speech: %w", err)
	}
	body, err := itemBytes(item)
	if err != nil {
		return nil, fmt.Errorf("captcha: encoding the competing speech: %w", err)
	}
	pcm, err := wavePCM(body)
	if err != nil {
		return nil, fmt.Errorf("captcha: reading the competing speech: %w", err)
	}
	// A very short draw is not worth mixing; the noise and pace still apply.
	if len(pcm) < length/babbleMinLengthDiv {
		return nil, nil
	}
	return pcm, nil
}

// mixBabble mixes src under dst at the given level, starting at a random offset
// so the competing speech does not always begin with the answer.
func mixBabble(dst, src []byte, level float64) error {
	if len(src) == 0 {
		return nil
	}
	offset, err := randomInt(0, len(src)-1)
	if err != nil {
		return err
	}
	for i := range dst {
		j := offset + i
		if j >= len(src) {
			break
		}
		dst[i] = clampSample(centre8(dst[i]) + int(float64(centre8(src[j]))*level) + 128)
	}
	return nil
}

// centre8 shifts an unsigned 8-bit sample onto the signed axis around zero.
func centre8(sample byte) int {
	return int(sample) - 128
}

// clampSample holds a signed sample inside the unsigned 8-bit range.
func clampSample(value int) byte {
	switch {
	case value < 0:
		return 0
	case value > 255:
		return 255
	default:
		return byte(value)
	}
}
