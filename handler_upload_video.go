package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Couldn't get video", err)
		return
	}

	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User is not video owner", err)
		return
	}

	fmt.Println("uploading video", videoID, "by user", userID)

	const maxMemory = 1 << 30

	if err := r.ParseMultipartForm(maxMemory); err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't read form data", err)
		return
	}

	file, header, err := r.FormFile("video")
	defer file.Close()

	contentType := header.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Invalid file type", err)
		return
	}

	tmp, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldn't create tmp file", err)
		return
	}

	if _, err := io.Copy(tmp, file); err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldn't copy uploaded file", err)
		return
	}

	aspectRatio, err := getVideoAspectRatio(tmp.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error processing video", err)
		return
	}

	fastStartTmp, err := processVideoForFastStart(tmp.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error processing fast start for video", err)
		return
	}

	fastStartFile, err := os.Open(fastStartTmp)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error opening file", err)
		return
	}

	defer os.Remove(fastStartTmp)
	defer fastStartFile.Close()

	tmp.Close()
	os.Remove(tmp.Name())

	idData := make([]byte, 32)
	if _, err := rand.Read(idData); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error generating id", err)
		return
	}
	objectID := base64.RawURLEncoding.EncodeToString(idData)

	var keyPrefix string
	switch aspectRatio {
	case "16:9":
		keyPrefix = "landscape"
	case "9:16":
		keyPrefix = "portrait"
	default:
		keyPrefix = "other"
	}

	key := fmt.Sprint(keyPrefix, "/", objectID, ".mp4")

	if _, err := cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &key,
		Body:        fastStartFile,
		ContentType: &mediaType,
	}); err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldn't upload file", err)
		return
	}

	videoURL := fmt.Sprintf("https://%v.s3.%v.amazonaws.com/%v", cfg.s3Bucket, cfg.s3Region, key)
	video.VideoURL = &videoURL

	if err := cfg.db.UpdateVideo(video); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video url", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}

func getVideoAspectRatio(filePath string) (string, error) {
	type FFProbeOutput struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}

	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	var buffer bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("ffprobe error: %v", err)
		fmt.Printf("std err: %s\n", stderr.String())
		return "", err
	}

	var output FFProbeOutput
	if err := json.Unmarshal(buffer.Bytes(), &output); err != nil {
		fmt.Println(err)
		return "", err
	}

	if len(output.Streams) == 0 {
		fmt.Println("no streams")
		return "", errors.New("no streams in ffprobe response")
	}

	width := float64(output.Streams[0].Width)
	height := float64(output.Streams[0].Height)
	ratio := width / height
	landscape := 16.0 / 9.0
	portrait := 9.0 / 16.0
	tolerance := 0.1

	var aspectRatio string
	if math.Abs(ratio-landscape) < tolerance {
		aspectRatio = "16:9"
	} else if math.Abs(ratio-portrait) < tolerance {
		aspectRatio = "9:16"
	} else {
		aspectRatio = "other"
	}

	return aspectRatio, nil
}

func processVideoForFastStart(filePath string) (string, error) {
	outputPath := fmt.Sprint(filePath, ".processing")
	cmd := exec.Command(
		"ffmpeg",
		"-i",
		filePath,
		"-c",
		"copy",
		"-movflags",
		"faststart",
		"-f",
		"mp4",
		outputPath,
	)

	if err := cmd.Run(); err != nil {
		return "", err
	}

	return outputPath, nil
}
