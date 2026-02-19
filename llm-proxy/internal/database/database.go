package database

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gluk-w/claworc/llm-proxy/internal/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func Init() error {
	dbPath := config.Cfg.DatabasePath
	dbDir := filepath.Dir(dbPath)
	if dbDir != "" {
		if err := os.MkdirAll(dbDir, 0755); err != nil {
			return fmt.Errorf("create db directory: %w", err)
		}
	}

	var err error
	DB, err = gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	sqlDB, err := DB.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB: %w", err)
	}
	if _, err := sqlDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return fmt.Errorf("set WAL mode: %w", err)
	}

	if err := DB.AutoMigrate(
		&InstanceToken{},
		&ProviderKey{},
		&UsageRecord{},
		&BudgetLimit{},
		&RateLimit{},
		&ModelPricing{},
	); err != nil {
		return fmt.Errorf("auto-migrate: %w", err)
	}

	if err := seedPricing(); err != nil {
		return fmt.Errorf("seed pricing: %w", err)
	}

	return nil
}

func Close() error {
	if DB != nil {
		sqlDB, err := DB.DB()
		if err != nil {
			return err
		}
		return sqlDB.Close()
	}
	return nil
}

func seedPricing() error {
	var count int64
	DB.Model(&ModelPricing{}).Count(&count)
	if count > 0 {
		return nil
	}

	pricing := []ModelPricing{
		// Anthropic
		{Provider: "anthropic", ModelPattern: "claude-opus-4", InputPriceMicro: 15000000, OutputPriceMicro: 75000000},
		{Provider: "anthropic", ModelPattern: "claude-sonnet-4", InputPriceMicro: 3000000, OutputPriceMicro: 15000000},
		{Provider: "anthropic", ModelPattern: "claude-haiku-4", InputPriceMicro: 800000, OutputPriceMicro: 4000000},
		{Provider: "anthropic", ModelPattern: "claude-3-5-sonnet", InputPriceMicro: 3000000, OutputPriceMicro: 15000000},
		{Provider: "anthropic", ModelPattern: "claude-3-5-haiku", InputPriceMicro: 800000, OutputPriceMicro: 4000000},
		{Provider: "anthropic", ModelPattern: "claude-3-opus", InputPriceMicro: 15000000, OutputPriceMicro: 75000000},
		// OpenAI
		{Provider: "openai", ModelPattern: "gpt-4o", InputPriceMicro: 2500000, OutputPriceMicro: 10000000},
		{Provider: "openai", ModelPattern: "gpt-4o-mini", InputPriceMicro: 150000, OutputPriceMicro: 600000},
		{Provider: "openai", ModelPattern: "gpt-4-turbo", InputPriceMicro: 10000000, OutputPriceMicro: 30000000},
		{Provider: "openai", ModelPattern: "o1", InputPriceMicro: 15000000, OutputPriceMicro: 60000000},
		{Provider: "openai", ModelPattern: "o1-mini", InputPriceMicro: 3000000, OutputPriceMicro: 12000000},
		{Provider: "openai", ModelPattern: "o3-mini", InputPriceMicro: 1100000, OutputPriceMicro: 4400000},
		// Google
		{Provider: "google", ModelPattern: "gemini-2.0-flash", InputPriceMicro: 75000, OutputPriceMicro: 300000},
		{Provider: "google", ModelPattern: "gemini-2.0-pro", InputPriceMicro: 1250000, OutputPriceMicro: 10000000},
		{Provider: "google", ModelPattern: "gemini-1.5-pro", InputPriceMicro: 1250000, OutputPriceMicro: 5000000},
		{Provider: "google", ModelPattern: "gemini-1.5-flash", InputPriceMicro: 75000, OutputPriceMicro: 300000},
		// Mistral
		{Provider: "mistral", ModelPattern: "mistral-large", InputPriceMicro: 2000000, OutputPriceMicro: 6000000},
		{Provider: "mistral", ModelPattern: "mistral-small", InputPriceMicro: 100000, OutputPriceMicro: 300000},
		// Groq
		{Provider: "groq", ModelPattern: "llama-3.3-70b", InputPriceMicro: 590000, OutputPriceMicro: 790000},
		{Provider: "groq", ModelPattern: "llama-3.1-8b", InputPriceMicro: 50000, OutputPriceMicro: 80000},
		// DeepSeek
		{Provider: "deepseek", ModelPattern: "deepseek-chat", InputPriceMicro: 270000, OutputPriceMicro: 1100000},
		{Provider: "deepseek", ModelPattern: "deepseek-reasoner", InputPriceMicro: 550000, OutputPriceMicro: 2190000},
		// xAI
		{Provider: "xai", ModelPattern: "grok-2", InputPriceMicro: 2000000, OutputPriceMicro: 10000000},
		// Cohere
		{Provider: "cohere", ModelPattern: "command-r-plus", InputPriceMicro: 2500000, OutputPriceMicro: 10000000},
		{Provider: "cohere", ModelPattern: "command-r", InputPriceMicro: 150000, OutputPriceMicro: 600000},
	}

	for _, p := range pricing {
		if err := DB.Create(&p).Error; err != nil {
			return fmt.Errorf("seed pricing %s/%s: %w", p.Provider, p.ModelPattern, err)
		}
	}
	return nil
}
