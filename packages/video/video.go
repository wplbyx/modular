package video

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// pgmMaxVal 是 16 位 PGM 的最大量化值（也是 header 中声明的 maxval）。
const pgmMaxVal = 255

// maxWallStretch 限制 L 型内折墙面的最大拉伸倍数。
// zRel = 1/(1-|tan θ|) 在 θ 趋近 45° 时趋于无穷、超过 45° 后变负，
// 物理上均无意义；此处把 |tan θ| 钳制到上限，保证 zRel ∈ (0, maxWallStretch]。
const maxWallStretch = 10.0

// ProjectionMapper 完全体对象
type ProjectionMapper struct {
	Width         int     // 视频宽度 (像素)
	Height        int     // 视频高度 (像素)
	FOVHorizontal float64 // 投影仪水平视场角 (角度制)
	VKeystone     float64 // 垂直梯形修正 (-0.5 ~ 0.5)
	HOffset       float64 // 左右偏航修正 (-0.5 ~ 0.5)
}

// GenerateRemapFiles 生成完美对齐的 PGM 映射文件。
// 输出的两张图 (mapX/mapY) 为每个目标像素指明应去源图采样的坐标，
// 可直接喂给 OpenCV cv::remap 或 FFmpeg remap 滤镜。
func (pm *ProjectionMapper) GenerateRemapFiles(outMapX, outMapY string) error {
	mapX, mapY, err := pm.computeMaps()
	if err != nil {
		return err
	}
	if err := pm.writePGM(outMapX, mapX); err != nil {
		return err
	}
	return pm.writePGM(outMapY, mapY)
}

func (pm *ProjectionMapper) computeMaps() (mapX, mapY []uint8, err error) { // 改为 uint8
	if pm.Width <= 0 || pm.Height <= 0 {
		return nil, nil, fmt.Errorf("video: invalid dimensions")
	}
	halfW := float64(pm.Width) / 2.0
	halfH := float64(pm.Height) / 2.0
	fovRad := pm.FOVHorizontal * math.Pi / 180.0

	mapX = make([]uint8, pm.Width*pm.Height) // 改为 uint8
	mapY = make([]uint8, pm.Width*pm.Height) // 改为 uint8

	for v := 0; v < pm.Height; v++ {
		yNorm := (halfH - float64(v)) / halfH
		for u := 0; u < pm.Width; u++ {
			xNorm := (float64(u) - halfW) / halfW

			theta := (xNorm - pm.HOffset) * (fovRad / 2.0)
			zRel := 1.0 + math.Abs(math.Tan(theta))
			keystoneFactor := 1.0 + (yNorm * pm.VKeystone)

			xSrcNorm := xNorm * zRel * keystoneFactor
			ySrcNorm := yNorm * zRel

			srcX := (xSrcNorm * halfW) + halfW
			srcY := halfH - (ySrcNorm * halfH)

			idx := v*pm.Width + u
			// 量化函数内部会自动按 pgmMaxVal (255) 进行缩放
			mapX[idx] = uint8(quantize(srcX, float64(pm.Width-1)))
			mapY[idx] = uint8(quantize(srcY, float64(pm.Height-1)))
		}
	}
	return mapX, mapY, nil
}

func (pm *ProjectionMapper) writePGM(filename string, data []uint8) error { // 改为 uint8
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	header := fmt.Sprintf("P5\n%d %d\n%d\n", pm.Width, pm.Height, pgmMaxVal)
	if _, err := file.WriteString(header); err != nil {
		return err
	}
	// 8位数据直接写入，彻底避开大端小端序的干扰
	if _, err := file.Write(data); err != nil {
		return err
	}
	return nil
}

func quantize(src, maxSrc float64) int {
	if src < 0 {
		src = 0
	} else if src > maxSrc {
		src = maxSrc
	}
	return int(math.Round((src / maxSrc) * float64(pgmMaxVal)))
}

// FFmpegRemapConfig 描述一次 remap 重映射渲染所需的 ffmpeg 参数。
// 零值字段会回退到默认值：Interp→lanczos、Codec→libx264、CRF→18；
// AudioCopy 默认通过 NewFFmpegRemapConfig 置为 true（直接拷贝音频）。
type FFmpegRemapConfig struct {
	Input     string        // 输入视频路径
	MapX      string        // X 映射文件路径（PGM，由 GenerateRemapFiles 产出）
	MapY      string        // Y 映射文件路径（PGM，由 GenerateRemapFiles 产出）
	Output    string        // 输出视频路径
	Interp    string        // 插值算法: linear|cubic|lanczos|nearest|spline；空则 lanczos
	Codec     string        // 视频编码器，空则 libx264
	CRF       int           // 恒定质量（libx264 取值 0-51），<=0 则 18
	AudioCopy bool          // true 时追加 -c:a copy，跳过音频重编码
	Docker    *DockerConfig // 非 nil 时以容器方式运行 ffmpeg，见 DockerConfig
}

// DockerConfig 描述以容器方式运行 ffmpeg 的参数。配置后 Command 会以
// `docker run --rm -v <hostPath>:<mountPath> [<extra>...] <image> ...` 形式运行，
// 并把宿主机路径自动重映射到容器内挂载点（容器为 Linux，统一正斜杠）。
type DockerConfig struct {
	Image     string   // 镜像名，空则 jrottenberg/ffmpeg:latest
	HostPath  string   // 宿主机挂载目录（Volume 源）；为空则不挂载，路径需已是容器内可见路径
	MountPath string   // 容器内挂载目录（Volume 目标），空则 /data
	ExtraArgs []string // 附加 docker run 参数，如 []string{"--network=host"}
}

// NewFFmpegRemapConfig 用四个必填路径构造一份带常用默认值（音频直接拷贝）的配置；
// 返回值可继续按需修改 Interp/Codec/CRF 等字段，再调用 Command 生成命令。
func NewFFmpegRemapConfig(input, mapX, mapY, output string) FFmpegRemapConfig {
	return FFmpegRemapConfig{
		Input:     input,
		MapX:      mapX,
		MapY:      mapY,
		Output:    output,
		AudioCopy: true,
	}
}

// Command 组装可直接打印或交给 shell 执行的 ffmpeg 命令行字符串，等价于:
//
// 未配置 Docker 时直接以本机 ffmpeg 运行：
//
//	ffmpeg -i <input> -i <mapX> -i <mapY> \
//	       -filter_complex "remap=interp=<interp>" -c:v <codec> -crf <crf> [-c:a copy] <output>
//
// 配置了 Docker（Docker 非 nil）时以容器方式运行，宿主机路径自动重映射到容器内挂载点：
//
//	docker run --rm -v <hostPath>:<mountPath> [<extra>...] <image> \
//	       -i <容器内 input> -i <容器内 mapX> -i <容器内 mapY> \
//	       -filter_complex "remap=interp=<interp>" -c:v <codec> -crf <crf> [-c:a copy] <容器内 output>
//
// 路径若含空格等 shell 元字符会自动转义。
func (cfg FFmpegRemapConfig) Command() string {
	interp := defaultStr(cfg.Interp, "lanczos")
	codec := defaultStr(cfg.Codec, "libx264")
	crf := cfg.CRF
	if crf <= 0 {
		crf = 18
	}
	input, mapX, mapY, output := cfg.Input, cfg.MapX, cfg.MapY, cfg.Output
	args := make([]string, 0, 16)
	if cfg.Docker != nil {
		image := defaultStr(cfg.Docker.Image, "jrottenberg/ffmpeg:latest")
		mountPath := defaultStr(cfg.Docker.MountPath, "/data")
		hostPath := cfg.Docker.HostPath

		// 宿主机路径映射为容器内路径（容器为 Linux，统一正斜杠）
		input = remapContainerPath(input, hostPath, mountPath)
		mapX = remapContainerPath(mapX, hostPath, mountPath)
		mapY = remapContainerPath(mapY, hostPath, mountPath)
		output = remapContainerPath(output, hostPath, mountPath)

		runArgs := []string{"docker", "run", "--rm"}
		if hostPath != "" {
			runArgs = append(runArgs, "-v", shellQuote(hostPath+":"+mountPath))
		}
		for _, a := range cfg.Docker.ExtraArgs {
			runArgs = append(runArgs, shellQuote(a))
		}
		runArgs = append(runArgs, image)
		// 容器镜像以 ffmpeg 为 entrypoint，命令名由镜像提供，无需再写出 ffmpeg。
		args = append(args, runArgs...)
	} else {
		args = append(args, "ffmpeg")
	}
	args = append(args,
		"-i", shellQuote(input),
		"-i", shellQuote(mapX),
		"-i", shellQuote(mapY),
		"-filter_complex", fmt.Sprintf(`"remap=interp=%s"`, interp),
		"-c:v", codec,
		"-crf", fmt.Sprintf("%d", crf),
	)
	if cfg.AudioCopy {
		args = append(args, "-c:a", "copy")
	}
	args = append(args, shellQuote(output))
	return strings.Join(args, " ")
}

// shellQuote 对含空格或 shell 元字符的参数做最小化 POSIX 转义；
// 不含特殊字符时原样返回，保持命令可读。
func shellQuote(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t\"'`$\\") {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}

// defaultStr 在 v 为空时返回 def，否则原样返回 v。
func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// remapContainerPath 把宿主机路径映射为容器内路径：仅当 path 位于 hostPath 下时
// 把前缀替换为 mountPath；不在挂载目录下的路径原样返回。统一输出正斜杠（容器为 Linux）。
func remapContainerPath(path, hostPath, mountPath string) string {
	p := filepath.ToSlash(path)
	if hostPath == "" {
		return p
	}
	hRoot := strings.TrimSuffix(filepath.ToSlash(hostPath), "/")
	if p == hRoot {
		return mountPath
	}
	if strings.HasPrefix(p, hRoot+"/") {
		return joinSlash(mountPath, strings.TrimPrefix(p, hRoot+"/"))
	}
	return p
}

// joinSlash 以正斜杠连接两段路径，去除拼接处多余分隔符。
func joinSlash(base, rel string) string {
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(rel, "/")
}
