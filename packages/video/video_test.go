package video

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// TestComputeMaps_DimensionsAndErrors 验证防御性参数校验
func TestComputeMaps_DimensionsAndErrors(t *testing.T) {
	tests := []struct {
		name    string
		mapper  ProjectionMapper
		wantErr bool
	}{
		{
			name:    "合法参数",
			mapper:  ProjectionMapper{Width: 1920, Height: 1080, FOVHorizontal: 60},
			wantErr: false,
		},
		{
			name:    "非法宽度",
			mapper:  ProjectionMapper{Width: 0, Height: 1080, FOVHorizontal: 60},
			wantErr: true,
		},
		{
			name:    "非法高度",
			mapper:  ProjectionMapper{Width: 1920, Height: -10, FOVHorizontal: 60},
			wantErr: true,
		},
		{
			name:    "非法FOV",
			mapper:  ProjectionMapper{Width: 1920, Height: 1080, FOVHorizontal: 0},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := tt.mapper.computeMaps()
			if (err != nil) != tt.wantErr {
				t.Errorf("computeMaps() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestComputeMaps_HourglassTrend 验证沙漏型核心趋势：中间压缩最狠，两端相对放大
func TestComputeMaps_HourglassTrend(t *testing.T) {
	// 初始化一个标准的 1920x1080 投影映射器，无安装误差
	pm := &ProjectionMapper{
		Width:         800,
		Height:        480,
		FOVHorizontal: 60.0,
	}

	_, mapY, err := pm.computeMaps()
	if err != nil {
		t.Fatalf("计算映射表失败: %v", err)
	}
	//t.Log(mapX, mapY)

	// 我们选取视频最顶部的一行 (v = 0)，观察不同水平位置 (u) 的 Y 轴采样行为
	// 原始视频的顶部物理行 Y 坐标对应的量化最大值应该是接近 0
	// 在沙漏型中，中央(u=960)被强力压缩，所以它会去原图更靠近中央的地方采样（即 mapY 的值会明显大于0，往中间收缩）
	// 而边缘(u=0)被放大，它采样的坐标应该非常接近原图的边缘（即 mapY 的值非常接近 0）

	centerIdx := 0*pm.Width + 960 // 顶部正中央
	leftIdx := 0*pm.Width + 0     // 顶部最左侧

	centerSrcY := mapY[centerIdx]
	leftSrcY := mapY[leftIdx]

	t.Logf("【沙漏型趋势测试】顶部边缘(u=0)采样Y值: %d, 顶部中央(u=960)采样Y值: %d", leftSrcY, centerSrcY)

	// 断言：中央采样的坐标值必须大于边缘采样的坐标值（说明中央被非线性地向内压缩了，符合沙漏型）
	if centerSrcY <= leftSrcY {
		t.Errorf("沙漏型数学趋势错误：中央采样的Y坐标(%d)应当大于边缘采样的Y坐标(%d)，这意味着中央没有受到更强的向内压缩", centerSrcY, leftSrcY)
	}
}

// TestComputeMaps_Symmetry 验证在无误差时，左右画面是否完美对称
func TestComputeMaps_Symmetry(t *testing.T) {
	pm := &ProjectionMapper{
		Width:         800,
		Height:        480,
		FOVHorizontal: 60.0,
	}

	mapX, mapY, err := pm.computeMaps()
	if err != nil {
		t.Fatalf("计算映射表失败: %v", err)
	}

	// 随机抽取中间一行的某两个对称点，例如左边 100 像素处，和右边对应 100 像素处
	v := 40 // 垂直正中央
	uLeft := 100
	uRight := pm.Width - 1 - uLeft // 1819

	idxLeft := v*pm.Width + uLeft
	idxRight := v*pm.Width + uRight

	// 对于 X 轴：左侧点和右侧点采样的原图位置应该关于中心(pgmMaxVal/2)镜像对称
	// 即：leftX + rightX ≈ pgmMaxVal
	leftX := float64(mapX[idxLeft])
	rightX := float64(mapX[idxRight])
	sumX := leftX + rightX

	// 对于 Y 轴：因为是完美的左右对称，两点的 Y 轴重采样坐标必须完全一致
	leftY := mapY[idxLeft]
	rightY := mapY[idxRight]

	t.Logf("【对称性测试】左侧点X: %.1f, 右侧点X: %.1f, 组合和: %.1f (期望接近 65535)", leftX, rightX, sumX)
	t.Logf("【对称性测试】左侧点Y: %d, 右侧点Y: %d", leftY, rightY)

	// 允许由于 math.Round 带来的 1 个量化单位的微小舍入误差
	if math.Abs(sumX-float64(pgmMaxVal)) > 1.0 {
		t.Errorf("左右X轴不满足镜像对称，和为 %.1f，期望 %d", sumX, pgmMaxVal)
	}

	if leftY != rightY {
		t.Errorf("左右Y轴不满足水平对称，左Y=%d, 右Y=%d", leftY, rightY)
	}
}

// TestFFmpegRemapCommand 验证 ffmpeg remap 命令行的拼装
func TestFFmpegRemapCommand(t *testing.T) {
	t.Run("默认参数", func(t *testing.T) {
		cfg := NewFFmpegRemapConfig("movie.mp4", "map_x.pgm", "map_y.pgm", "output.mp4")
		got := cfg.Command()
		want := `ffmpeg -i /data/origin.mp4 -i /data/remap_x.pgm -i /data/remap_y.pgm -filter_complex "remap=interp=lanczos" -c:v libx264 -crf 18 -c:a copy /data/remap_output.mp4`
		if got != want {
			t.Errorf("命令拼装不匹配\n got: %s\nwant: %s", got, want)
		}
	})

	t.Run("自定义插值编码与CRF", func(t *testing.T) {
		cfg := NewFFmpegRemapConfig("in.mp4", "x.pgm", "y.pgm", "out.mp4")
		cfg.Interp = "cubic"
		cfg.Codec = "libx265"
		cfg.CRF = 23
		got := cfg.Command()
		want := `ffmpeg -i in.mp4 -i x.pgm -i y.pgm -filter_complex "remap=interp=cubic" -c:v libx265 -crf 23 -c:a copy out.mp4`
		if got != want {
			t.Errorf("命令拼装不匹配\n got: %s\nwant: %s", got, want)
		}
	})

	t.Run("关闭音频拷贝", func(t *testing.T) {
		cfg := NewFFmpegRemapConfig("in.mp4", "x.pgm", "y.pgm", "out.mp4")
		cfg.AudioCopy = false
		got := cfg.Command()
		want := `ffmpeg -i in.mp4 -i x.pgm -i y.pgm -filter_complex "remap=interp=lanczos" -c:v libx264 -crf 18 out.mp4`
		if got != want {
			t.Errorf("命令拼装不匹配\n got: %s\nwant: %s", got, want)
		}
	})

	t.Run("路径含空格自动转义", func(t *testing.T) {
		cfg := NewFFmpegRemapConfig("my movie.mp4", "map x.pgm", "map y.pgm", "out put.mp4")
		got := cfg.Command()
		if !strings.Contains(got, "'my movie.mp4'") {
			t.Errorf("输入路径未正确转义: %s", got)
		}
		if !strings.Contains(got, "'out put.mp4'") {
			t.Errorf("输出路径未正确转义: %s", got)
		}
	})
}

func TestFFmpegDockerCommand(t *testing.T) {
	t.Run("默认镜像与挂载", func(t *testing.T) {
		cfg := NewFFmpegRemapConfig("/data/video/movie.mp4", "/data/video/map_x.pgm", "/data/video/map_y.pgm", "/data/video/output.mp4")
		cfg.Docker = &DockerConfig{HostPath: "/data/video"}
		got := cfg.Command()
		want := `docker run --rm -v /data/video:/data jrottenberg/ffmpeg:latest -i /data/movie.mp4 -i /data/map_x.pgm -i /data/map_y.pgm -filter_complex "remap=interp=lanczos" -c:v libx264 -crf 18 -c:a copy /data/output.mp4`
		if got != want {
			t.Errorf("命令拼装不匹配\n got: %s\nwant: %s", got, want)
		}
	})

	t.Run("自定义镜像挂载点与参数", func(t *testing.T) {
		cfg := NewFFmpegRemapConfig("/work/in.mp4", "/work/x.pgm", "/work/y.pgm", "/work/out.mp4")
		cfg.Docker = &DockerConfig{
			Image:     "linuxserver/ffmpeg:latest",
			HostPath:  "/work",
			MountPath: "/media",
			ExtraArgs: []string{"--network=host", "-e", "TZ=Asia/Shanghai"},
		}
		got := cfg.Command()
		if !strings.Contains(got, "docker run --rm -v /work:/media --network=host -e TZ=Asia/Shanghai linuxserver/ffmpeg:latest") {
			t.Errorf("docker 前缀拼装错误: %s", got)
		}
		if !strings.Contains(got, "-i /media/in.mp4") {
			t.Errorf("输入路径未映射到容器内: %s", got)
		}
		if !strings.Contains(got, "/media/out.mp4") {
			t.Errorf("输出路径未映射到容器内: %s", got)
		}
	})

	t.Run("不在挂载目录下的路径原样保留", func(t *testing.T) {
		cfg := NewFFmpegRemapConfig("/other/movie.mp4", "/work/x.pgm", "/work/y.pgm", "/work/out.mp4")
		cfg.Docker = &DockerConfig{HostPath: "/work"}
		got := cfg.Command()
		if !strings.Contains(got, "-i /other/movie.mp4") {
			t.Errorf("非挂载目录路径应原样保留: %s", got)
		}
		if !strings.Contains(got, "-i /data/x.pgm") {
			t.Errorf("挂载目录路径应映射: %s", got)
		}
	})
}

// TestGenerateRemapFiles_XAndYPGM 验证 GenerateRemapFiles 正确生成 *_x.pgm 与 *_y.pgm，
// 且文件内容与 computeMaps 完全一致（header + 16 位大端数据 round-trip）。
func TestGenerateRemapFiles_XAndYPGM(t *testing.T) {
	mapX := filepath.Join(".", "remap_x.pgm")
	mapY := filepath.Join(".", "remap_y.pgm")

	pm := &ProjectionMapper{
		Width:         800,
		Height:        480,
		FOVHorizontal: 60.0,
	}
	if err := pm.GenerateRemapFiles(mapX, mapY); err != nil {
		t.Fatalf("GenerateRemapFiles 失败: %v", err)
	}

	// 两个文件都应存在且非空
	for _, name := range []string{mapX, mapY} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatalf("生成的文件不存在 %s: %v", name, err)
		}
		if info.Size() == 0 {
			t.Errorf("文件为空: %s", name)
		}
	}

	// 文件名遵循 *_x.pgm / *_y.pgm 约定
	if !strings.HasSuffix(mapX, "_x.pgm") {
		t.Errorf("X 映射文件名缺少 _x.pgm 后缀: %s", mapX)
	}
	if !strings.HasSuffix(mapY, "_y.pgm") {
		t.Errorf("Y 映射文件名缺少 _y.pgm 后缀: %s", mapY)
	}

	// 读回并与 computeMaps 对比，确保 round-trip 字节一致
	wantX, wantY, err := pm.computeMaps()
	if err != nil {
		t.Fatalf("computeMaps 失败: %v", err)
	}

	gotX, w, h, err := readPGM16(mapX)
	if err != nil {
		t.Fatalf("读取 X 映射失败 %s: %v", mapX, err)
	}
	if w != pm.Width || h != pm.Height {
		t.Errorf("X PGM 尺寸 = %dx%d, 期望 %dx%d", w, h, pm.Width, pm.Height)
	}
	if !reflect.DeepEqual(gotX, wantX) {
		t.Errorf("X 映射数据与 computeMaps 不一致 (len got=%d want=%d)", len(gotX), len(wantX))
	}

	gotY, _, _, err := readPGM16(mapY)
	if err != nil {
		t.Fatalf("读取 Y 映射失败 %s: %v", mapY, err)
	}
	if !reflect.DeepEqual(gotY, wantY) {
		t.Errorf("Y 映射数据与 computeMaps 不一致 (len got=%d want=%d)", len(gotY), len(wantY))
	}
}

// readPGM16 解析 16 位大端 P5 PGM，返回像素数据与 header 声明的宽高。
// 手动逐 token 解析（PGM 允许空格/换行混用），maxval 之后恰好一个空白字符即二进制数据起点。
func readPGM16(path string) (data []uint16, width, height int, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, 0, err
	}
	pos := 0
	// nextToken 跳过前导空白，读取到下一个空白为止的连续字符。
	nextToken := func() (string, error) {
		for pos < len(raw) && isPGMSpace(raw[pos]) {
			pos++
		}
		start := pos
		for pos < len(raw) && !isPGMSpace(raw[pos]) {
			pos++
		}
		if pos == start {
			return "", fmt.Errorf("unexpected EOF in PGM header")
		}
		return string(raw[start:pos]), nil
	}
	magic, err := nextToken()
	if err != nil {
		return nil, 0, 0, fmt.Errorf("解析 magic: %w", err)
	}
	if magic != "P5" {
		return nil, 0, 0, fmt.Errorf("非 P5 格式: %q", magic)
	}
	wTok, err := nextToken()
	if err != nil {
		return nil, 0, 0, err
	}
	hTok, err := nextToken()
	if err != nil {
		return nil, 0, 0, err
	}
	mTok, err := nextToken()
	if err != nil {
		return nil, 0, 0, err
	}
	width, err = strconv.Atoi(wTok)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("非法宽度 %q: %w", wTok, err)
	}
	height, err = strconv.Atoi(hTok)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("非法高度 %q: %w", hTok, err)
	}
	maxval, err := strconv.Atoi(mTok)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("非法 maxval %q: %w", mTok, err)
	}
	if maxval != pgmMaxVal {
		return nil, 0, 0, fmt.Errorf("非预期 maxval %d (期望 %d)", maxval, pgmMaxVal)
	}
	// maxval token 之后恰好一个空白字符分隔 header 与二进制数据。
	if pos < len(raw) && isPGMSpace(raw[pos]) {
		pos++
	}
	data = make([]uint16, width*height)
	if err := binary.Read(bytes.NewReader(raw[pos:]), binary.BigEndian, data); err != nil {
		return nil, 0, 0, fmt.Errorf("读取像素数据: %w", err)
	}
	return data, width, height, nil
}

// isPGMSpace 判断 PGM header 中的空白分隔符（空格/Tab/换行）。
func isPGMSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
