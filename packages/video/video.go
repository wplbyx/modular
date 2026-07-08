package video

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
)

const (
	// DefaultHorizontalFOVDeg 是当前投影源的水平发散全角。
	// 该角度描述投影光锥左右边缘之间的夹角，不再使用 DMax 这类经验距离参数。
	DefaultHorizontalFOVDeg = 44.8

	// invalidRemapCoord 写入一个源视频尺寸之外的坐标，让 FFmpeg remap 用 fill 颜色填充。
	// 这里不能把越界点 clamp 到边缘，否则会把顶部/底部像素拉成一条线。
	invalidRemapCoord uint16 = 65535
)

// DistortionModel 镜头畸变模型参数
type DistortionModel struct {
	K1, K2, K3 float64 // 径向畸变系数
	P1, P2     float64 // 切向畸变系数
}

// ProjectionMapper 投影映射核心对象
type ProjectionMapper struct {
	Width      int             // 视频宽度 (像素)
	Height     int             // 视频高度 (像素)
	HFovDeg    float64         // 投影源水平发散全角；为 0 时使用 DefaultHorizontalFOVDeg
	Distortion DistortionModel // 镜头畸变参数
}

// GenerateRemap16Files 生成匹配 FFmpeg remap 滤镜的 16位 PGM 映射文件。
func (pm *ProjectionMapper) GenerateRemap16Files(outMapXFile, outMapYFile string) error {
	fovDeg, err := pm.effectiveHorizontalFOVDeg()
	if err != nil {
		return err
	}

	targetW, targetH := pm.Width, pm.Height

	mapXData := make([]uint16, targetW*targetH)
	mapYData := make([]uint16, targetW*targetH)

	centerX := float64(targetW-1) / 2.0
	centerY := float64(targetH-1) / 2.0
	tanHalfFOV := math.Tan((fovDeg / 2.0) * math.Pi / 180.0)

	for y := 0; y < targetH; y++ {
		for x := 0; x < targetW; x++ {
			idx := y*targetW + x
			srcX, srcY, ok := pm.mapLScreenPixel(x, y, tanHalfFOV)
			if ok {
				// 这里的镜头畸变和 L 屏几何预扭曲是两个独立环节。
				// Distortion 为零时直接跳过；非零时只对仍在源视频范围内的采样点做修正。
				srcX, srcY = pm.undistort(srcX, srcY, centerX, centerY)
			}
			mapXData[idx], mapYData[idx] = encodeRemap16(srcX, srcY, targetW, targetH, ok)
		}
	}

	// 写入 PGM
	if err := writePGM16(outMapXFile, targetW, targetH, mapXData); err != nil {
		return fmt.Errorf("failed to write X map: %w", err)
	}
	if err := writePGM16(outMapYFile, targetW, targetH, mapYData); err != nil {
		return fmt.Errorf("failed to write Y map: %w", err)
	}

	return nil
}

func (pm *ProjectionMapper) effectiveHorizontalFOVDeg() (float64, error) {
	if pm.Width <= 1 {
		return 0, fmt.Errorf("video width must be greater than 1: %d", pm.Width)
	}
	if pm.Height <= 1 {
		return 0, fmt.Errorf("video height must be greater than 1: %d", pm.Height)
	}
	if pm.Width > int(invalidRemapCoord) {
		return 0, fmt.Errorf("video width exceeds 16-bit remap range: %d", pm.Width)
	}
	if pm.Height > int(invalidRemapCoord) {
		return 0, fmt.Errorf("video height exceeds 16-bit remap range: %d", pm.Height)
	}

	fovDeg := pm.HFovDeg
	if fovDeg == 0 {
		fovDeg = DefaultHorizontalFOVDeg
	}
	if fovDeg < 0 {
		return 0, fmt.Errorf("horizontal FOV must not be negative: %g", fovDeg)
	}
	if fovDeg >= 180 {
		return 0, fmt.Errorf("horizontal FOV must be less than 180 degrees: %g", fovDeg)
	}
	return fovDeg, nil
}

func (pm *ProjectionMapper) mapLScreenPixel(x, y int, tanHalfFOV float64) (float64, float64, bool) {
	widthMax := float64(pm.Width - 1)
	heightMax := float64(pm.Height - 1)

	// u/v 是输出帧坐标归一化到 [-1, 1] 后的位置。
	// 输出帧就是投影机要播放的预扭曲画面；remap 需要反查该输出像素应从源视频哪里取样。
	u := 2.0*float64(x)/widthMax - 1.0
	v := 2.0*float64(y)/heightMax - 1.0

	// tanHalfFOV = tan(水平发散全角 / 2)，表示投影光锥半角对应的水平射线斜率。
	// 在 90 度等边 L 屏、投影源位于角平分线且屏幕足够大的假设下，真实投影距离会在归一化中抵消，
	// 因此不再需要 DMax 这类经验距离参数。
	q := math.Abs(u) * tanHalfFOV

	// srcXNorm 是 L 屏展开方向上的源视频横坐标。
	// q/(1+q) 来自投影射线与 90 度 L 屏平面的交点比例；
	// 再除以边缘值 T/(1+T)，保证左右边缘仍映射到源视频左右边界。
	edgeFoldRatio := tanHalfFOV / (1.0 + tanHalfFOV)
	var srcXNorm float64
	if edgeFoldRatio != 0 {
		srcXNorm = math.Copysign((q/(1.0+q))/edgeFoldRatio, u)
	}

	// srcYNorm 表达垂直方向的预压缩。
	// 中轴线最远，因此中心列需要压缩最多；越靠近左右两侧，屏幕越近，压缩逐渐减小。
	// 当中心上下超出源视频范围时，保留越界，让 remap 填黑，形成预期的横向沙漏区域。
	srcYNorm := v * (1.0 + tanHalfFOV) / (1.0 + q)

	srcX := (srcXNorm + 1.0) * widthMax / 2.0
	srcY := (srcYNorm + 1.0) * heightMax / 2.0
	return srcX, srcY, isFinite(srcX) && isFinite(srcY)
}

func encodeRemap16(srcX, srcY float64, width, height int, ok bool) (uint16, uint16) {
	if !ok || srcX < 0 || srcX > float64(width-1) || srcY < 0 || srcY > float64(height-1) {
		return invalidRemapCoord, invalidRemapCoord
	}
	return uint16(math.Round(srcX)), uint16(math.Round(srcY))
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// FFmpegRemapConfig 描述一次 classic CPU remap 调用需要的路径与编码参数。
// classic remap 只支持 fill/format 选项，不支持 remap_opencl 的 interp 选项。
type FFmpegRemapConfig struct {
	Input  string // 输入视频路径
	MapX   string // X 映射 PGM；应由 GenerateRemap16Files 生成
	MapY   string // Y 映射 PGM；应由 GenerateRemap16Files 生成
	Output string // 输出视频路径
	Codec  string // 视频编码器，空则 libx264
	CRF    int    // 恒定质量，<=0 则 18
	Preset string // 编码预设，空则 fast
}

// Command 组装兼容 FFmpeg classic remap 的命令行。
// map PGM 文件保持 P5/65535/BigEndian 写入；不要在滤镜图里强转 gray16le，
// 那是 FFmpeg 解码后的内部像素格式，不是 PGM 文件的字节序设置。
func (cfg FFmpegRemapConfig) Command() string {
	codec := defaultStr(cfg.Codec, "libx264")
	preset := defaultStr(cfg.Preset, "fast")
	crf := cfg.CRF
	if crf <= 0 {
		crf = 18
	}

	args := []string{
		"ffmpeg",
		"-y",
		"-i", shellQuote(cfg.Input),
		"-loop", "1", "-i", shellQuote(cfg.MapX),
		"-loop", "1", "-i", shellQuote(cfg.MapY),
		"-filter_complex", shellQuote("[0:v]format=yuv444p[vid];[vid][1:v][2:v]remap=fill=black,format=yuv420p[outv]"),
		"-map", shellQuote("[outv]"),
		"-map", shellQuote("0:a?"),
		"-c:v", codec,
		"-crf", fmt.Sprintf("%d", crf),
		"-preset", preset,
		"-c:a", "copy",
	}
	args = append(args, "-shortest", shellQuote(cfg.Output))
	return strings.Join(args, " ")
}

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func shellQuote(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t\"'`$\\;[]?&|()<>") {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}

// undistort 镜头畸变变换（将理想投影坐标转换为受镜头畸变影响的实际视频像素坐标）
func (pm *ProjectionMapper) undistort(srcX, srcY, cx, cy float64) (float64, float64) {
	// 如果没有任何镜头畸变参数，直接返回理想几何坐标，防止不必要的浮点运算
	if pm.Distortion.K1 == 0 && pm.Distortion.K2 == 0 && pm.Distortion.K3 == 0 && pm.Distortion.P1 == 0 && pm.Distortion.P2 == 0 {
		return srcX, srcY
	}

	// 归一化到相机坐标系
	f := (float64(pm.Width) + float64(pm.Height)) / 2.0
	x := (srcX - cx) / f
	y := (srcY - cy) / f

	// 修正：采用标准的不动点迭代法（Fixed-point iteration）求解逆畸变
	distX, distY := x, y
	for i := 0; i < 8; i++ { // 8次迭代足以收敛到极致精度
		r2 := distX*distX + distY*distY

		// 径向畸变因子
		radial := 1.0 + pm.Distortion.K1*r2 + pm.Distortion.K2*r2*r2 + pm.Distortion.K3*r2*r2*r2
		// 切向畸变因子
		tangX := 2.0*pm.Distortion.P1*distX*distY + pm.Distortion.P2*(r2+2.0*distX*distX)
		tangY := 2.0*pm.Distortion.P2*distX*distY + pm.Distortion.P1*(r2+2.0*distY*distY)

		// 计算当前猜测下的理想坐标
		idealX := distX*radial + tangX
		idealY := distY*radial + tangY

		// 修正猜测值：当前值 - (计算出的理想值 - 真实的理想值)
		distX = distX - (idealX - x)
		distY = distY - (idealY - y)
	}

	// 映射回像素坐标
	return distX*f + cx, distY*f + cy
}

// writePGM16 将 uint16 数组写入为符合 FFmpeg 要求的 16位 PGM P5 格式文件。
// PGM 规范要求 maxval 大于 255 时每个样本按大端字节序写入；这里不能改成小端。
// FFmpeg 会先按 PGM 解码，再把 16-bit 单通道帧交给 remap，滤镜图中也不需要 format=gray16le。
func writePGM16(filename string, w, h int, data []uint16) (err error) {
	if len(data) != w*h {
		return fmt.Errorf("PGM data length mismatch: got %d, want %d", len(data), w*h)
	}

	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, file.Close())
	}()

	writer := bufio.NewWriter(file)

	// PGM P5 头部。16位格式的最大值必须声明为 65535
	if _, err := fmt.Fprintf(writer, "P5\n%d %d\n65535\n", w, h); err != nil {
		return err
	}

	// 写入二进制像素数据
	buf := make([]byte, 2)
	for _, val := range data {
		binary.BigEndian.PutUint16(buf, val)
		_, err := writer.Write(buf)
		if err != nil {
			return err
		}
	}

	return writer.Flush()
}
