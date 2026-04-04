package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/lib/pq"
)

var DB *sql.DB

func OpenDB() error {
	connStr := fmt.Sprintf("host=%s port=5432 user=%s password=%s dbname=kumbula_system sslmode=disable",
		POSTGRES_HOST, POSTGRES_USER, POSTGRES_PASS)
	var err error
	DB, err = sql.Open("postgres", connStr)
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}
	if err := DB.Ping(); err != nil {
		return fmt.Errorf("db ping: %w", err)
	}
	DB.SetMaxOpenConns(25)
	DB.SetMaxIdleConns(5)
	log.Printf("Connected to PostgreSQL")
	return nil
}
