package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/urfave/cli"
	"github.com/zero-gravity-labs/zerog-data-avail/common/aws/dynamodb"
	"github.com/zero-gravity-labs/zerog-data-avail/common/aws/s3"
	"github.com/zero-gravity-labs/zerog-data-avail/common/geth"
	"github.com/zero-gravity-labs/zerog-data-avail/common/logging"
	"github.com/zero-gravity-labs/zerog-data-avail/disperser"
	"github.com/zero-gravity-labs/zerog-data-avail/disperser/batcher"
	"github.com/zero-gravity-labs/zerog-data-avail/disperser/batcher/dispatcher"
	"github.com/zero-gravity-labs/zerog-data-avail/disperser/cmd/batcher/flags"
	"github.com/zero-gravity-labs/zerog-data-avail/disperser/common/blobstore"
	"github.com/zero-gravity-labs/zerog-data-avail/disperser/common/inmem"
	"github.com/zero-gravity-labs/zerog-data-avail/disperser/encoder"
)

var (
	// version is the version of the binary.
	version   string
	gitCommit string
	gitDate   string
)

func main() {
	app := cli.NewApp()
	app.Flags = flags.Flags
	app.Version = fmt.Sprintf("%s-%s-%s", version, gitCommit, gitDate)
	app.Name = "batcher"
	app.Usage = "ZGDA Batcher"
	app.Description = "Service for creating a batch from queued blobs, distributing coded chunks to nodes, and confirming onchain"

	app.Action = RunBatcher
	err := app.Run(os.Args)
	if err != nil {
		log.Fatalf("application failed: %v", err)
	}

	select {}
}

func RunBatcher(ctx *cli.Context) error {
	config := NewConfig(ctx)

	logger, err := logging.GetLogger(config.LoggerConfig)
	if err != nil {
		return err
	}
	// dispatcher
	dispatcher, err := dispatcher.NewDispatcher(&dispatcher.Config{
		EthClientURL:      config.EthClientConfig.RPCURL,
		PrivateKeyString:  config.EthClientConfig.PrivateKeyString,
		StorageNodeConfig: config.StorageNodeConfig,
	}, logger)
	if err != nil {
		return err
	}

	// eth clients
	client, err := geth.NewClient(config.EthClientConfig, logger)
	if err != nil {
		logger.Error("Cannot create chain.Client", "err", err)
		return err
	}

	rpcClient, err := rpc.Dial(config.EthClientConfig.RPCURL)
	if err != nil {
		return err
	}

	// blob store
	var queue disperser.BlobStore

	if config.AwsClientConfig.UseMemDb {
		queue = inmem.NewBlobStore()
	} else {
		bucketName := config.BlobstoreConfig.BucketName
		s3Client, err := s3.NewClient(context.Background(), config.AwsClientConfig, logger)
		if err != nil {
			return err
		}
		logger.Info("Initialized S3 client", "bucket", bucketName)

		dynamoClient, err := dynamodb.NewClient(config.AwsClientConfig, logger)
		if err != nil {
			return err
		}
		blobMetadataStore := blobstore.NewBlobMetadataStore(dynamoClient, logger, config.BlobstoreConfig.TableName, 0)
		queue = blobstore.NewSharedStorage(bucketName, s3Client, blobMetadataStore, logger)
	}

	metrics := batcher.NewMetrics(config.MetricsConfig.HTTPPort, logger)

	// encoder
	if len(config.BatcherConfig.EncoderSocket) == 0 {
		return fmt.Errorf("encoder socket must be specified")
	}
	encoderClient, err := encoder.NewEncoderClient(config.BatcherConfig.EncoderSocket, config.TimeoutConfig.EncodingTimeout)
	if err != nil {
		return err
	}

	// confirmer
	confirmer, err := batcher.NewConfirmer(config.EthClientConfig, config.StorageNodeConfig, queue, config.BatcherConfig.MaxNumRetriesPerBlob, logger, metrics)
	if err != nil {
		return err
	}

	//finalizer
	finalizer := batcher.NewFinalizer(config.TimeoutConfig.ChainReadTimeout, config.BatcherConfig.FinalizerInterval, queue, client, rpcClient, config.BatcherConfig.MaxNumRetriesPerBlob, logger)

	//batcher
	batcher, err := batcher.NewBatcher(config.BatcherConfig, config.TimeoutConfig, queue, dispatcher, encoderClient, finalizer, confirmer, logger, metrics)
	if err != nil {
		return err
	}

	// Enable Metrics Block
	if config.MetricsConfig.EnableMetrics {
		httpSocket := fmt.Sprintf(":%s", config.MetricsConfig.HTTPPort)
		metrics.Start(context.Background())
		logger.Info("Enabled metrics for Batcher", "socket", httpSocket)
	}

	err = batcher.Start(context.Background())
	if err != nil {
		return err
	}

	return nil

}