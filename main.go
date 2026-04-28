package main

// Stub imports to anchor go.mod dependencies.
// This file will be replaced when the real entry point is implemented.
import (
	_ "github.com/go-chi/chi/v5"
	_ "github.com/golang-jwt/jwt/v5"
	_ "github.com/golang-migrate/migrate/v4"
	_ "github.com/google/uuid"
	_ "github.com/jackc/pgx/v5"
	_ "github.com/pgvector/pgvector-go"
	_ "github.com/redis/go-redis/v9"
	_ "go.uber.org/zap"
)

func main() {}
