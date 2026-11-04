package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	maxUploadBytes = 20 << 20 // 20MB
	maxImageSide   = 1600
)

// saveUploadedFiles 把表单里的 files 字段全部入库。
func (s *Server) saveUploadedFiles(r *http.Request, txID int64) error {
	if r.MultipartForm == nil {
		return nil
	}
	for _, fh := range r.MultipartForm.File["files"] {
		key, mime, size, err := s.saveImage(fh)
		if err != nil {
			return err
		}
		if err := s.st.AddAttachment(txID, key, mime, size); err != nil {
			return err
		}
	}
	return nil
}

// saveImage 解码、等比缩小（最长边 1600）后统一转 JPEG 存储，返回相对 key。
func (s *Server) saveImage(fh *multipart.FileHeader) (key, mime string, size int64, err error) {
	if fh.Size > maxUploadBytes {
		return "", "", 0, errors.New("图片超过 20MB")
	}
	f, err := fh.Open()
	if err != nil {
		return "", "", 0, err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return "", "", 0, errors.New("不支持的图片格式，请使用 JPG/PNG/WebP")
	}
	b := img.Bounds()
	if b.Dx()*b.Dy() > 60_000_000 {
		return "", "", 0, errors.New("图片尺寸过大")
	}

	dst := img
	if b.Dx() > maxImageSide || b.Dy() > maxImageSide {
		w, h := b.Dx(), b.Dy()
		if w >= h {
			h = h * maxImageSide / w
			w = maxImageSide
		} else {
			w = w * maxImageSide / h
			h = maxImageSide
		}
		if w < 1 {
			w = 1
		}
		if h < 1 {
			h = 1
		}
		rgba := image.NewRGBA(image.Rect(0, 0, w, h))
		// 透明区域铺白底，避免 JPEG 变黑。
		xdraw.Draw(rgba, rgba.Bounds(), &image.Uniform{C: color.White}, image.Point{}, xdraw.Src)
		xdraw.CatmullRom.Scale(rgba, rgba.Bounds(), img, b, xdraw.Over, nil)
		dst = rgba
	}

	month := time.Now().Format("200601")
	dir := filepath.Join(s.uploads, month)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", 0, err
	}
	id := randomHex(12)
	rel := month + "/" + id + ".jpg"
	full := filepath.Join(s.uploads, filepath.FromSlash(rel))
	out, err := os.OpenFile(full, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", "", 0, err
	}
	defer out.Close()
	if err := jpeg.Encode(out, dst, &jpeg.Options{Quality: 85}); err != nil {
		return "", "", 0, err
	}
	st, err := out.Stat()
	if err != nil {
		return rel, "image/jpeg", 0, nil
	}
	return rel, "image/jpeg", st.Size(), nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("150405.000000")))
	}
	return hex.EncodeToString(b)
}

// upload 处理前端异步上传，返回 {key,url}。
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "上传解析失败"})
		return
	}
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "没有收到文件"})
		return
	}
	key, mime, size, err := s.saveImage(files[0])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"key": key, "mime": mime, "size": size, "url": "/media/" + key, "name": files[0].Filename,
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
