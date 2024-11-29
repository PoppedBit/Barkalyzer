package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/hajimehoshi/go-mp3"
	"github.com/joho/godotenv"
	"github.com/poppedbit/Barkalyzer/helpers"
)

var PathDelimiter = string(filepath.Separator)

func main() {

	// ENV
	err := godotenv.Load()
	if err != nil {
		log.Fatalf("Error loading .env file")
	}

	router := mux.NewRouter()

	router.HandleFunc("/upload-and-analyze", UploadAndAnalyzeHandler).Methods("POST")
	router.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	router.PathPrefix("/webfonts/").Handler(http.StripPrefix("/webfonts/", http.FileServer(http.Dir("static/webfonts"))))
	router.HandleFunc("/{uploadId}", GetAppHandler).Methods("GET")
	router.HandleFunc("/", GetAppHandler).Methods("GET")

	// Server
	port := os.Getenv("PORT")
	println("Server running at http://localhost:" + port)
	log.Fatal(http.ListenAndServe(":"+port, router))
}

type Upload struct {
	ID   string
	Date string
	File string
}

type AppData struct {
	helpers.BaseTemplateData
	UploadId string
	Uploads  []Upload
}

func GetAppHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	uploadId := vars["uploadId"]

	templates := []string{
		"templates/index.html",
	}

	tmpl, err := helpers.ParseFullPage(templates...)
	if err != nil {
		log.Println(err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Get all uploads, each dir inside the uploads directory is an upload
	uploadsDir := os.Getenv("UPLOADS")
	uploads, err := os.ReadDir(uploadsDir)
	if err != nil {
		log.Println(err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Get upload details
	var uploadsData []Upload
	for _, upload := range uploads {
		uploadDir := uploadsDir + PathDelimiter + upload.Name()
		uploadFiles, err := os.ReadDir(uploadDir)
		if err != nil {
			log.Println(err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		uploadData := Upload{
			ID:   upload.Name(),
			Date: "TODO",
			File: uploadFiles[0].Name(),
		}
		uploadsData = append(uploadsData, uploadData)
	}

	data := AppData{
		UploadId: uploadId,
		Uploads:  uploadsData,
	}
	data.BaseTemplateData.Init(r)

	err = tmpl.ExecuteTemplate(w, "base", data)
	if err != nil {
		log.Println(err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}

type AmplitudeData struct {
	Timestamp float64 `json:"timestamp"`
	Amplitude float64 `json:"amplitude"`
}

func UploadAndAnalyzeHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseMultipartForm(10 << 20) // 10 MB

	uploadedFile, handler, err := r.FormFile("file")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer uploadedFile.Close()

	runUUID := uuid.New().String()

	runDir := os.Getenv("UPLOADS") + PathDelimiter + runUUID
	err = os.MkdirAll(runDir, os.ModePerm)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	audioFilePath := runDir + PathDelimiter + handler.Filename

	audioFile, err := os.Create(audioFilePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	defer audioFile.Close()
	_, err = io.Copy(audioFile, uploadedFile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// I guess reopen it?
	audioFile, err = os.Open(audioFilePath)
	if err != nil {
		log.Fatalf("failed to open file: %v", err)
	}
	defer audioFile.Close()

	// Analyze file for peaks in volume
	decoder, err := mp3.NewDecoder(audioFile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Get audio sample rate (default is usually 44.1kHz for MP3)
	const sampleRate = 44100 // Adjust if your file has a different sample rate
	bytesPerSample := 2      // Assuming 16-bit PCM audio (2 bytes per sample)
	samplesPerSecond := sampleRate
	bytesPerSecond := samplesPerSecond * bytesPerSample

	// Buffer to hold PCM data for 1 second
	buffer := make([]byte, bytesPerSecond)

	// Array to store volume data
	var amplitudeData []AmplitudeData
	currentTimestamp := 0.0

	// Process audio data
	for {
		// Read 1 second of data
		n, err := decoder.Read(buffer)
		if n > 0 {
			// Calculate peak volume for this chunk
			amplitude := calculatePeakAmplitude(buffer[:n])
			amplitudeData = append(amplitudeData, AmplitudeData{
				Timestamp: currentTimestamp,
				Amplitude: amplitude,
			})
			currentTimestamp += 1.0
		}
		if err != nil {
			break // EOF or error
		}
	}

	outputFilePath := runDir + PathDelimiter + "output.csv"
	outputFile, err := os.Create(outputFilePath)
	if err != nil {
		panic(err)
	}
	defer outputFile.Close()

	// Create a CSV writer
	writer := csv.NewWriter(outputFile)
	defer writer.Flush()

	// Write the header
	header := []string{"Timestamp", "Amplitude"}
	if err := writer.Write(header); err != nil {
		panic(err)
	}

	for _, data := range amplitudeData {
		row := []string{
			fmt.Sprintf("%f", data.Timestamp),
			fmt.Sprintf("%f", data.Amplitude),
		}
		if err := writer.Write(row); err != nil {
			panic(err)
		}
	}

	w.Header().Set("HX-Redirect", "/"+runUUID)
	w.WriteHeader(http.StatusOK) // Status 200 OK
}

func calculatePeakAmplitude(pcm []byte) float64 {
	var maxAmplitude float64

	// Iterate through PCM data (16-bit samples)
	for i := 0; i < len(pcm); i += 2 {
		// Convert two bytes into a 16-bit sample
		sample := int16(pcm[i]) | int16(pcm[i+1])<<8
		amplitude := math.Abs(float64(sample))
		if amplitude > maxAmplitude {
			maxAmplitude = amplitude
		}
	}

	// Normalize to a range (optional, e.g., 0 to 1)
	// maxAmplitude /= 32768.0 // Uncomment to normalize for 16-bit audio

	return maxAmplitude
}
