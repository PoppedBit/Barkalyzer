package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/go-audio/wav"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/hajimehoshi/go-mp3"
	"github.com/joho/godotenv"
	"github.com/poppedbit/Barkalyzer/helpers"
)

var PathDelimiter = string(filepath.Separator)

type AmplitudeData struct {
	Timestamp int
	Amplitude int
}

type Upload struct {
	ID   string
	Date string
	File string
}

type UploadMetadata struct {
	MaxAmplitude int
}

type UploadData struct {
	ID       string
	RawData  string
	Metadata UploadMetadata
}

type AppData struct {
	helpers.BaseTemplateData
	Uploads        []Upload
	SelectedUpload UploadData
}

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

		// Get created date
		audioFile := uploadFiles[0]

		uploadData := Upload{
			ID:   upload.Name(),
			Date: "TODO",
			File: audioFile.Name(),
		}
		uploadsData = append(uploadsData, uploadData)
	}

	var selectedUpload UploadData
	if uploadId != "" {

		outputFilePath := uploadsDir + PathDelimiter + uploadId + PathDelimiter + "output.csv"
		file, err := os.Open(outputFilePath)
		if err != nil {
			log.Println(err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		defer file.Close()

		reader := csv.NewReader(file)

		var rawData []AmplitudeData
		var maxAmplitude int = 0
		header := false

		for {
			record, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				log.Println(err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			if !header {
				header = true
				continue
			}

			timestamp, err := strconv.Atoi(record[0])
			if err != nil {
				log.Println(err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			amplitude, err := strconv.Atoi(record[1])
			if err != nil {
				log.Println(err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			rawData = append(rawData, AmplitudeData{
				Timestamp: timestamp,
				Amplitude: amplitude,
			})

			if amplitude > maxAmplitude {
				maxAmplitude = amplitude
			}
		}

		rawDataJSON, _ := json.Marshal(rawData)

		metadata := UploadMetadata{
			MaxAmplitude: maxAmplitude,
		}

		selectedUpload = UploadData{
			ID:       uploadId,
			RawData:  string(rawDataJSON),
			Metadata: metadata,
		}
	}

	data := AppData{
		Uploads:        uploadsData,
		SelectedUpload: selectedUpload,
	}
	data.BaseTemplateData.Init(r)

	err = tmpl.ExecuteTemplate(w, "base", data)
	if err != nil {
		log.Println(err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
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

	// Determine file type
	fileType := filepath.Ext(handler.Filename)
	if fileType == ".wav" {
		err = analyzeWAVAmplitude(runUUID, audioFilePath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else if fileType == ".mp3" {
		err = analyzeMP3Amplitude(runUUID, audioFilePath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "Unsupported file type", http.StatusBadRequest)
		return
	}

	w.Header().Set("HX-Redirect", "/"+runUUID)
	w.WriteHeader(http.StatusOK) // Status 200 OK
}

func analyzeMP3Amplitude(runUUID string, filePath string) error {
	// I guess reopen it?
	audioFile, err := os.Open(filePath)
	if err != nil {
		log.Fatalf("failed to open file: %v", err)
	}
	defer audioFile.Close()

	// Analyze file for peaks in volume
	decoder, err := mp3.NewDecoder(audioFile)
	if err != nil {
		return err
	}

	// Get audio sample rate (default is usually 44.1kHz for MP3)
	const sampleRate = 48000 // Adjust if your file has a different sample rate
	bytesPerSample := 2      // Assuming 16-bit PCM audio (2 bytes per sample)
	samplesPerSecond := sampleRate
	bytesPerSecond := samplesPerSecond * bytesPerSample

	// Buffer to hold PCM data for 1 second
	buffer := make([]byte, bytesPerSecond)

	// Array to store volume data
	var amplitudeData []AmplitudeData
	currentTimestamp := 0

	// Process audio data
	for {
		// Read 1 second of data
		n, err := decoder.Read(buffer)
		if n > 0 {
			// Calculate peak volume for this chunk
			amplitude := calculatePeakAmplitude(buffer[:n])
			amplitudeData = append(amplitudeData, AmplitudeData{
				Timestamp: currentTimestamp,
				Amplitude: int(amplitude),
			})
			currentTimestamp += 1
		}
		if err != nil {
			break // EOF or error
		}
	}

	writeAmplitudeDataToCSV(runUUID, amplitudeData)

	return nil
}

func analyzeWAVAmplitude(runUUID, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := wav.NewDecoder(file)
	if !decoder.IsValidFile() {
		return fmt.Errorf("invalid WAV file")
	}

	buf, err := decoder.FullPCMBuffer()
	if err != nil {
		return err
	}

	// Initialize a map to store the maximum amplitude for each second
	maxAmplitudePerSecond := make(map[int]int)

	// Iterate through the samples and update the map
	for i, sample := range buf.Data {
		second := i / int(buf.Format.SampleRate)
		amplitude := int(sample)
		if amplitude < 0 {
			amplitude = -amplitude
		}
		if amplitude > maxAmplitudePerSecond[second] {
			maxAmplitudePerSecond[second] = amplitude
		}
	}

	// Convert the map to a slice of AmplitudeData
	var amplitudeData []AmplitudeData
	for second, amplitude := range maxAmplitudePerSecond {
		amplitudeData = append(amplitudeData, AmplitudeData{
			Timestamp: second,
			Amplitude: amplitude,
		})
	}

	writeAmplitudeDataToCSV(runUUID, amplitudeData)

	return nil
}

func writeAmplitudeDataToCSV(runUUID string, data []AmplitudeData) error {
	runDir := os.Getenv("UPLOADS") + PathDelimiter + runUUID
	outputFilePath := runDir + PathDelimiter + "output.csv"
	outputFile, err := os.Create(outputFilePath)
	if err != nil {
		return err
	}
	defer outputFile.Close()

	// Create a CSV writer
	writer := csv.NewWriter(outputFile)
	defer writer.Flush()

	// Write the header
	header := []string{"Timestamp", "Amplitude"}
	if err := writer.Write(header); err != nil {
		return err
	}

	// Sort data by timestamp
	sort.Slice(data, func(i, j int) bool {
		return data[i].Timestamp < data[j].Timestamp
	})

	for _, d := range data {
		row := []string{
			fmt.Sprintf("%d", d.Timestamp),
			fmt.Sprintf("%d", d.Amplitude),
		}
		if err := writer.Write(row); err != nil {
			panic(err)
		}
	}

	return nil
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
