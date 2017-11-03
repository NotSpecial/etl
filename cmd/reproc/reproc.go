// Package main defines a command line tool for submitting date
// ranges for reprocessing
package main

// TODO - note about setting up batch table and giving permission.

/*
Strategies...
  1. Work from a prefix, or range of prefixes.
  2. Work from a date range
  3. Work from a month prefix, but explicitly iterate over days.
      maybe use a separate goroutine for each date?

Usage:




*/

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/spaolacci/murmur3"
	"google.golang.org/api/iterator"

	"cloud.google.com/go/storage"
)

var (
	fProject   = flag.String("project", "mlab-oti", "Project containing queues.")
	fQueue     = flag.String("queue", "etl-ndt-batch-", "Base of queue name.")
	fNumQueues = flag.Int("num_queues", 5, "Number of queues.  Normally determined by listing queues.")
	fBucket    = flag.String("bucket", "archive-mlab-oti", "Source bucket.")
	fExper     = flag.String("experiment", "ndt", "Experiment prefix, trailing slash optional")
	fMonth     = flag.String("month", "", "Single month spec, as YYYY/MM")
	fDay       = flag.String("day", "", "Single day spec, as YYYY/MM/DD")

	errCount      int32
	storageClient *storage.Client
	bucket        *storage.BucketHandle

	hasher = murmur3.New32()
)

func init() {
	// Always prepend the filename and line number.
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}

func postOne(queue string, bucket string, fn string) error {
	reqStr := fmt.Sprintf("http://queue-pusher-dot-%s.appspot.com/receiver?queue=%s&filename=gs://%s/%s", *fProject, queue, bucket, fn)
	resp, err := http.Get(reqStr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("http error: " + resp.Status)
	}

	return nil
}

// Post all items in an ObjectIterator into specific
// queue.
func postDay(wg *sync.WaitGroup, queue string, it *storage.ObjectIterator) {
	defer wg.Done()
	log.Printf("%+v\n", it)
	for o, err := it.Next(); err != iterator.Done; o, err = it.Next() {
		if err != nil {
			log.Println(err)
			ec := atomic.AddInt32(&errCount, 1)
			if ec > 10 {
				panic(err)
			}
		}

		err = postOne(queue, *fBucket, o.Name)
		if err != nil {
			log.Println(err)
			ec := atomic.AddInt32(&errCount, 1)
			if ec > 10 {
				panic(err)
			}
		}
	}
}

func queueFor(prefix string) string {
	hasher.Reset()
	hasher.Write([]byte(prefix))
	hash := hasher.Sum32()
	return fmt.Sprintf("%s%d", *fQueue, int(hash)%*fNumQueues)
}

func day(prefix string) {
	log.Println(prefix)
	q := storage.Query{
		Delimiter: "/",
		// TODO - validate.
		Prefix: prefix,
	}
	it := bucket.Objects(context.Background(), &q)
	var wg sync.WaitGroup
	wg.Add(1)
	go postDay(&wg, queueFor(prefix), it)
	log.Println("Waiting")
	wg.Wait()
}

func month(prefix string) {
	log.Println(prefix)
	q := storage.Query{
		Delimiter: "/",
		// TODO - validate.
		Prefix: prefix,
	}
	it := bucket.Objects(context.Background(), &q)

	var wg sync.WaitGroup
	for o, err := it.Next(); err != iterator.Done; o, err = it.Next() {
		if err != nil {
			log.Println(err)
		}
		//		log.Printf("%+v\n", o)
		if o.Prefix != "" {
			q := storage.Query{
				Delimiter: "/",
				// TODO - validate.
				Prefix: o.Prefix,
			}
			it := bucket.Objects(context.Background(), &q)
			queue := queueFor(o.Prefix)
			wg.Add(1)
			go postDay(&wg, queue, it)
		} else {
			log.Println("Skipping: ", o.Name)
		}
	}
	log.Println("Waiting")
	wg.Wait()
}

func main() {
	flag.Parse()

	var err error
	storageClient, err = storage.NewClient(context.Background())
	if err != nil {
		log.Println(err)
		panic(err)
	}

	bucket = storageClient.Bucket(*fBucket)
	attr, err := bucket.Attrs(context.Background())
	if err != nil {
		log.Println(err)
		panic(err)
	}
	log.Println(attr)

	if *fMonth != "" {
		month(*fExper + "/" + *fMonth + "/")
	} else if *fDay != "" {
		day(*fExper + "/" + *fDay + "/")
	}
}
