package store

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// Store wraps the DynamoDB client + table name. Repos hang off it.
type Store struct {
	db    *dynamodb.Client
	table string
}

func New(cfg aws.Config, table string) *Store {
	return &Store{db: dynamodb.NewFromConfig(cfg), table: table}
}

func (s *Store) Table() string { return s.table }

// Ping verifies table reachability at startup (fail fast on misconfig).
func (s *Store) Ping(ctx context.Context) error {
	_, err := s.db.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: &s.table})
	return err
}
