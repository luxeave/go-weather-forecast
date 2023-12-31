package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	_ "github.com/lib/pq"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
)

type GeoResponse struct {
	// A list of results; we only need the first one
	Results []LatLong `json:"results"`
}

type LatLong struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type WeatherData struct{}
type WeatherResponse struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Timezone  string  `json:"timezone"`
	Hourly    struct {
		Time          []string  `json:"time"`
		Temperature2m []float64 `json:"temperature_2m"`
	} `json:"hourly"`
}

type WeatherDisplay struct {
	City      string
	Forecasts []Forecast
}

type Forecast struct {
	Date        string
	Temperature string
}

func extractWeatherData(city string, rawWeather string) (WeatherDisplay, error) {
	var weatherResponse WeatherResponse
	if err := json.Unmarshal([]byte(rawWeather), &weatherResponse); err != nil {
		return WeatherDisplay{}, fmt.Errorf("error decoding weather response: %w", err)
	}

	var forecasts []Forecast
	for i, t := range weatherResponse.Hourly.Time {
		date, err := time.Parse("2006-01-02T15:04", t)
		if err != nil {
			return WeatherDisplay{}, err
		}
		forecast := Forecast{
			Date:        date.Format("Mon 15:04"),
			Temperature: fmt.Sprintf("%.1f°C", weatherResponse.Hourly.Temperature2m[i]),
		}
		forecasts = append(forecasts, forecast)
	}
	return WeatherDisplay{
		City:      city,
		Forecasts: forecasts,
	}, nil
}

func fetchLatLong(city string) (*LatLong, error) {
	endpoint := fmt.Sprintf("https://geocoding-api.open-meteo.com/v1/search?name=%s&count=1&language=en&format=json", url.QueryEscape(city))
	resp, err := http.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("error making request to Geo API: %w", err)
	}
	defer resp.Body.Close()

	var response GeoResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("error decoding response: %w", err)
	}

	// response.Results is a list of results, but we only need the first one
	if len(response.Results) < 1 {
		return nil, errors.New("no results found")
	}

	return &response.Results[0], nil
}

func getLatLong(db *sqlx.DB, name string) (*LatLong, error) {
	var latLong *LatLong
	err := db.Get(&latLong, "SELECT lat, long FROM cities WHERE name = $1", name)
	if err == nil {
		return latLong, nil
	}

	latLong, err = fetchLatLong(name)
	if err != nil {
		return nil, err
	}

	err = insertCity(db, name, *latLong)
	if err != nil {
		return nil, err
	}

	return latLong, nil
}

func getWeather(latLong LatLong) (string, error) {
	endpoint := fmt.Sprintf("https://api.open-meteo.com/v1/forecast?latitude=%.6f&longitude=%.6f&hourly=temperature_2m", latLong.Latitude, latLong.Longitude)
	resp, err := http.Get(endpoint)
	if err != nil {
		return "", fmt.Errorf("error making request to Weather API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading response body: %w", err)
	}

	return string(body), nil
}

func insertCity(db *sqlx.DB, name string, latLong LatLong) error {
	_, err := db.Exec("INSERT INTO cities (name, lat, long) VALUES ($1, $2, $3)", name, latLong.Latitude, latLong.Longitude)
	return err
}

func main() {
	r := gin.Default()
	r.LoadHTMLGlob("views/*")

	db := sqlx.MustConnect("postgres", os.Getenv("DATABASE_URL"))

	r.GET("/weather", func(c *gin.Context) {
		city := c.Query("city")
		latlong, err := getLatLong(db, city)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		weather, err := getWeather(*latlong)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// print out weather
		fmt.Println(weather)

		//////// NEW CODE STARTS HERE ////////
		weatherDisplay, err := extractWeatherData(city, weather)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.HTML(http.StatusOK, "weather.html", weatherDisplay)
		//////////////////////////////////////
	})

	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", nil)
	})

	r.Run()
}
