package audio

import (
	"bytes"
	"errors"
	"io"
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
)

// sine returns seconds of a 440 Hz tone as 16-bit little-endian PCM.
func sine(rate, channels int, seconds float64, amplitude float64) []byte {
	n := int(float64(rate) * seconds)
	out := make([]byte, 0, n*channels*2)
	for i := range n {
		v := int16(amplitude * math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
		for range channels {
			out = append(out, byte(uint16(v)), byte(uint16(v)>>8))
		}
	}
	return out
}

// decode reads an Ogg Opus file back and returns its mono samples at 48 kHz
// and the number of packets.
func decode(t *testing.T, file []byte) (samples []float32, packets int) {
	t.Helper()
	r, err := ogg.NewReader(bytes.NewReader(file))
	if err != nil {
		t.Fatalf("not a valid Ogg Opus stream: %v", err)
	}
	dec, err := gopus.NewDecoder(gopus.DecoderConfig{SampleRate: 48000, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]float32, 5760)
	for {
		pkt, _, err := r.ReadPacket()
		if errors.Is(err, io.EOF) {
			return samples, packets
		}
		if err != nil {
			t.Fatalf("read packet: %v", err)
		}
		n, err := dec.Decode(pkt, buf)
		if err != nil {
			t.Fatalf("decode packet: %v", err)
		}
		samples = append(samples, buf[:n]...)
		packets++
	}
}

func rms(x []float32) float64 {
	if len(x) == 0 {
		return 0
	}
	var sum float64
	for _, v := range x {
		sum += float64(v) * float64(v)
	}
	return math.Sqrt(sum / float64(len(x)))
}

func TestPCMToOggOpusRoundTrip(t *testing.T) {
	// Half a second of a clear tone at the usual speech-model rate.
	file, err := PCMToOggOpus(sine(24000, 1, 0.5, 12000), 24000, 1)
	if err != nil {
		t.Fatalf("PCMToOggOpus() error = %v", err)
	}

	if !bytes.HasPrefix(file, []byte("OggS")) || !bytes.Contains(file[:100], []byte("OpusHead")) || !bytes.Contains(file[:200], []byte("OpusTags")) {
		t.Fatal("output is not an Ogg Opus stream with OpusHead and OpusTags headers")
	}
	// A 20 ms frame is 50 packets a second.
	got, packets := decode(t, file)
	if packets < 24 || packets > 27 {
		t.Errorf("packets = %d, want about 25 for half a second", packets)
	}
	// The tone must survive: audible, and about as loud as a 12000-amplitude sine.
	if level := rms(got[len(got)/4 : 3*len(got)/4]); level < 0.15 || level > 0.45 {
		t.Errorf("decoded RMS level = %.3f; the tone should come through at a sensible level", level)
	}
	// Length is preserved to within a couple of frames (decoded at 48 kHz).
	if dur := float64(len(got)) / 48000; dur < 0.45 || dur > 0.6 {
		t.Errorf("decoded duration = %.2fs, want about 0.5s", dur)
	}
	if len(file) > 12000 {
		t.Errorf("%d bytes for half a second of speech-grade audio is too large", len(file))
	}
}

func TestSilenceStaysSilent(t *testing.T) {
	file, err := PCMToOggOpus(make([]byte, 24000*2), 24000, 1) // 0.5 s of zeros
	if err != nil {
		t.Fatal(err)
	}
	got, _ := decode(t, file)
	if level := rms(got); level > 0.01 {
		t.Errorf("silence decoded at RMS %.3f", level)
	}
}

func TestOtherRatesAndStereo(t *testing.T) {
	tests := []struct {
		name     string
		rate     int
		channels int
	}{
		{"16 kHz mono (native rate)", 16000, 1},
		{"48 kHz mono", 48000, 1},
		{"22.05 kHz mono (resampled)", 22050, 1},
		{"44.1 kHz stereo (mixed down and resampled)", 44100, 2},
		{"24 kHz stereo", 24000, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := PCMToOggOpus(sine(tt.rate, tt.channels, 0.4, 10000), tt.rate, tt.channels)
			if err != nil {
				t.Fatalf("PCMToOggOpus() error = %v", err)
			}
			got, _ := decode(t, file)
			if dur := float64(len(got)) / 48000; dur < 0.35 || dur > 0.5 {
				t.Errorf("duration = %.2fs, want about 0.4s (resampling must keep the pitch and length)", dur)
			}
			if level := rms(got[len(got)/4 : 3*len(got)/4]); level < 0.1 {
				t.Errorf("decoded RMS level = %.3f; audio was lost", level)
			}
		})
	}
}

func TestPitchSurvivesResampling(t *testing.T) {
	// Count zero crossings of the decoded middle section: a 440 Hz tone crosses
	// zero about 880 times a second, whatever rate it started at.
	file, err := PCMToOggOpus(sine(22050, 1, 1.0, 12000), 22050, 1)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := decode(t, file)
	mid := got[12000:36000] // half a second of 48 kHz audio
	crossings := 0
	for i := 1; i < len(mid); i++ {
		if (mid[i-1] < 0) != (mid[i] < 0) {
			crossings++
		}
	}
	if crossings < 400 || crossings > 480 { // ~440 expected in half a second
		t.Errorf("zero crossings = %d in 0.5s, want about 440 (pitch changed)", crossings)
	}
}

func TestTrailingPartialSampleIsDropped(t *testing.T) {
	pcm := append(sine(24000, 1, 0.2, 8000), 0x7f) // one stray byte
	if _, err := PCMToOggOpus(pcm, 24000, 1); err != nil {
		t.Errorf("a trailing odd byte should be ignored, got %v", err)
	}
}

func TestInvalidInput(t *testing.T) {
	tests := []struct {
		name     string
		pcm      []byte
		rate     int
		channels int
	}{
		{"empty", nil, 24000, 1},
		{"single stray byte", []byte{1}, 24000, 1},
		{"three channels", make([]byte, 600), 24000, 3},
		{"zero channels", make([]byte, 600), 24000, 0},
		{"absurd rate", make([]byte, 600), 3, 1},
		{"absurd rate high", make([]byte, 600), 1000000, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := PCMToOggOpus(tt.pcm, tt.rate, tt.channels); err == nil {
				t.Error("PCMToOggOpus() succeeded, want error")
			}
		})
	}
}

func TestVeryShortAudio(t *testing.T) {
	// Fewer samples than one frame: padded, encoded, and still a valid stream.
	file, err := PCMToOggOpus(sine(24000, 1, 0.005, 10000), 24000, 1)
	if err != nil {
		t.Fatalf("PCMToOggOpus() error = %v", err)
	}
	if _, packets := decode(t, file); packets < 1 {
		t.Error("no packets in a short clip")
	}
}
