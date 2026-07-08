package video

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateRemap16File_GenerateFiles(t *testing.T) {
	generateTestMaps(t, ProjectionMapper{
		Width:   800,
		Height:  480,
		HFovDeg: DefaultHorizontalFOVDeg,
	})
}

func TestGenerateRemap16Files_WritesPGM16BigEndianForFFmpeg(t *testing.T) {
	const (
		width  = 800
		height = 480
	)

	dir := t.TempDir()
	mapXFile := filepath.Join(dir, "remap_x.pgm")
	mapYFile := filepath.Join(dir, "remap_y.pgm")
	mapper := ProjectionMapper{
		Width:   width,
		Height:  height,
		HFovDeg: DefaultHorizontalFOVDeg,
	}

	require.NoError(t, mapper.GenerateRemap16Files(mapXFile, mapYFile))

	header, payload := readPGM16Raw(t, mapXFile, width, height)
	require.Equal(t, []byte(fmt.Sprintf("P5\n%d %d\n65535\n", width, height)), header)
	require.Len(t, payload, width*height*2)

	mapX := decodePGM16Payload(payload)
	rightCenter := (height/2)*width + (width - 1)
	require.Equal(t, uint16(width-1), mapX[rightCenter])
	require.Equal(t, uint16(width-1), binary.BigEndian.Uint16(payload[rightCenter*2:]))
	require.NotEqual(t, uint16(width-1), binary.LittleEndian.Uint16(payload[rightCenter*2:]))

	_, payloadY := readPGM16Raw(t, mapYFile, width, height)
	mapY := decodePGM16Payload(payloadY)
	centerTop := width / 2
	require.Equal(t, invalidRemapCoord, mapY[width/2])
	require.Equal(t, invalidRemapCoord, binary.BigEndian.Uint16(payloadY[centerTop*2:]))
}

func TestFFmpegRemapCommand_UsesCompatibleFormats(t *testing.T) {
	cfg := FFmpegRemapConfig{
		Input:  "/data/origin.mp4",
		MapX:   "/data/remap_x.pgm",
		MapY:   "/data/remap_y.pgm",
		Output: "/data/remap_output1.mp4",
	}

	got := cfg.Command()

	require.Contains(t, got, "[0:v]format=yuv444p[vid]")
	require.Contains(t, got, "[vid][1:v][2:v]remap=fill=black,format=yuv420p[outv]")
	require.Contains(t, got, "-filter_complex '[0:v]format=yuv444p[vid];[vid][1:v][2:v]remap=fill=black,format=yuv420p[outv]'")
	require.NotContains(t, got, "gray16le")
	require.NotContains(t, got, "interp=")
	require.Contains(t, got, "-map '[outv]'")
	require.Contains(t, got, "-map '0:a?'")
	require.Contains(t, got, "-c:a copy")
	require.Contains(t, got, "-shortest")
}

func TestGenerateRemap16Files_DefaultFOVMatchesExplicitDefault(t *testing.T) {
	const (
		width  = 801
		height = 481
	)

	defaultX, defaultY := generateTestMaps(t, ProjectionMapper{
		Width:  width,
		Height: height,
	})
	explicitX, explicitY := generateTestMaps(t, ProjectionMapper{
		Width:   width,
		Height:  height,
		HFovDeg: DefaultHorizontalFOVDeg,
	})

	require.Equal(t, explicitX, defaultX)
	require.Equal(t, explicitY, defaultY)
}

func TestGenerateRemap16Files_FOVGeometryMapsLScreen(t *testing.T) {
	const (
		width  = 801
		height = 481
	)

	mapX, mapY := generateTestMaps(t, ProjectionMapper{
		Width:   width,
		Height:  height,
		HFovDeg: DefaultHorizontalFOVDeg,
	})

	centerX := width / 2
	centerY := height / 2

	require.Equal(t, uint16(centerX), mapAt(mapX, width, centerX, centerY))
	require.Equal(t, uint16(centerY), mapAt(mapY, width, centerX, centerY))

	require.Equal(t, uint16(0), mapAt(mapX, width, 0, centerY))
	require.Equal(t, uint16(width-1), mapAt(mapX, width, width-1, centerY))
	require.Equal(t, uint16(centerY), mapAt(mapY, width, 0, centerY))
	require.Equal(t, uint16(centerY), mapAt(mapY, width, width-1, centerY))

	require.Equal(t, invalidRemapCoord, mapAt(mapY, width, centerX, 0))
	require.Equal(t, invalidRemapCoord, mapAt(mapY, width, centerX, height-1))
	require.Equal(t, uint16(0), mapAt(mapY, width, 0, 0))
	require.Equal(t, uint16(height-1), mapAt(mapY, width, width-1, height-1))
}

func TestGenerateRemap16Files_FOVGeometryIsSymmetric(t *testing.T) {
	const (
		width  = 801
		height = 481
	)

	mapX, mapY := generateTestMaps(t, ProjectionMapper{
		Width:   width,
		Height:  height,
		HFovDeg: DefaultHorizontalFOVDeg,
	})

	centerY := height / 2
	for _, leftX := range []int{1, 100, 250, 399} {
		rightX := width - 1 - leftX
		leftSrcX := int(mapAt(mapX, width, leftX, centerY))
		rightSrcX := int(mapAt(mapX, width, rightX, centerY))

		require.InDelta(t, width-1, leftSrcX+rightSrcX, 1)
		require.Equal(t, mapAt(mapY, width, leftX, centerY), mapAt(mapY, width, rightX, centerY))
	}
}

func TestGenerateRemap16Files_RejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		mapper ProjectionMapper
	}{
		{
			name: "width too small",
			mapper: ProjectionMapper{
				Width:  1,
				Height: 480,
			},
		},
		{
			name: "height too small",
			mapper: ProjectionMapper{
				Width:  800,
				Height: 1,
			},
		},
		{
			name: "negative fov",
			mapper: ProjectionMapper{
				Width:   800,
				Height:  480,
				HFovDeg: -1,
			},
		},
		{
			name: "straight angle fov",
			mapper: ProjectionMapper{
				Width:   800,
				Height:  480,
				HFovDeg: 180,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			err := tt.mapper.GenerateRemap16Files(
				filepath.Join(dir, "x.pgm"),
				filepath.Join(dir, "y.pgm"),
			)
			require.Error(t, err)
		})
	}
}

func generateTestMaps(t *testing.T, mapper ProjectionMapper) ([]uint16, []uint16) {
	t.Helper()
	dir := t.TempDir()
	mapXFile := filepath.Join(dir, "remap_x.pgm")
	mapYFile := filepath.Join(dir, "remap_y.pgm")

	require.NoError(t, mapper.GenerateRemap16Files(mapXFile, mapYFile))

	return readPGM16(t, mapXFile, mapper.Width, mapper.Height),
		readPGM16(t, mapYFile, mapper.Width, mapper.Height)
}

func readPGM16(t *testing.T, filename string, width, height int) []uint16 {
	t.Helper()
	_, payload := readPGM16Raw(t, filename, width, height)
	return decodePGM16Payload(payload)
}

func readPGM16Raw(t *testing.T, filename string, width, height int) ([]byte, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filename)
	require.NoError(t, err)

	header := []byte(fmt.Sprintf("P5\n%d %d\n65535\n", width, height))
	require.True(t, bytes.HasPrefix(raw, header), "PGM header mismatch")

	payload := raw[len(header):]
	require.Len(t, payload, width*height*2)
	return header, payload
}

func decodePGM16Payload(payload []byte) []uint16 {
	data := make([]uint16, len(payload)/2)
	for i := range data {
		data[i] = binary.BigEndian.Uint16(payload[i*2 : i*2+2])
	}
	return data
}

func mapAt(data []uint16, width, x, y int) uint16 {
	return data[y*width+x]
}
