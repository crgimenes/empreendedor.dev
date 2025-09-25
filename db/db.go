package db

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	"github.com/crgimenes/empreendedor.dev/config"
)

var Storage *Postgres

type Postgres struct {
	DB *sqlx.DB
}

type Transaction struct {
	//Pg *Postgres
	tx *sqlx.Tx
}

func New() (*Postgres, error) {
	pg := &Postgres{}
	var err error
	pg.DB, err = open(config.Cfg.DatabaseURL)

	return pg, err
}

func open(dbsource string) (db *sqlx.DB, err error) {
	db, err = sqlx.Open("postgres", dbsource)
	if err != nil {
		err = fmt.Errorf("error open db: %v", err)
		return
	}

	err = db.Ping()
	if err != nil {
		log.Fatalln(err)
	}

	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(30)
	db.SetConnMaxLifetime(5 * time.Minute)

	return
}

func (pg *Postgres) BeginTransaction() (*Transaction, error) {
	var err error
	tx, err := pg.DB.Beginx()
	if err != nil {
		return nil, err
	}
	t := Transaction{
		tx: tx,
	}

	return &t, nil
}

func (pg Transaction) Commit() error {
	err := pg.tx.Commit()
	if err != nil {
		pg.Rollback()
	}
	pg.tx = nil
	return err
}

func (pg Transaction) Rollback() error {
	err := pg.tx.Rollback()
	pg.tx = nil
	return err
}

func (pg Transaction) Select(dest any, query string, args ...any) error {
	return pg.tx.Select(dest, query, args...)
}

func (pg Transaction) Query(query string, args ...any) (*sql.Rows, error) {
	return pg.tx.Query(query, args...)
}

func (pg Transaction) Get(dest any, query string, args ...any) error {
	return pg.tx.Get(dest, query, args...)
}

func (pg Transaction) Exec(query string, args ...any) error {
	_, err := pg.tx.Exec(query, args...)
	return err
}

func (pg Transaction) QueryRow(query string, args ...any) *sql.Row {
	return pg.tx.QueryRow(query, args...)
}

func (pg *Postgres) QueryRow(query string, args ...any) *sql.Row {
	return pg.DB.QueryRow(query, args...)
}

func (pg *Postgres) Query(query string, args ...any) (*sql.Rows, error) {
	return pg.DB.Query(query, args...)
}

func (pg *Postgres) Close() {
	err := pg.DB.Close()
	if err != nil {
		log.Println(err)
	}
}

func (pg *Postgres) Exec(query string, args ...any) error {
	_, err := pg.DB.Exec(query, args...)
	return err
}
