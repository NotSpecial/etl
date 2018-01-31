# etl
| branch | travis-ci | coveralls | report-card |
|--------|-----------|-----------|-------------|
| master | [![Travis Build Status](https://travis-ci.org/m-lab/etl.svg?branch=master)](https://travis-ci.org/m-lab/etl) | [![Coverage Status](https://coveralls.io/repos/m-lab/etl/badge.svg?branch=master)](https://coveralls.io/github/m-lab/etl?branch=master) | |
| integration | [![Travis Build Status](https://travis-ci.org/m-lab/etl.svg?branch=integration)](https://travis-ci.org/m-lab/etl) | [![Coverage Status](https://coveralls.io/repos/m-lab/etl/badge.svg?branch=integration)](https://coveralls.io/github/m-lab/etl?branch=integration) |
[![Go Report
Card](https://goreportcard.com/badge/github.com/m-lab/etl)](https://goreportcard.com/report/github.com/m-lab/etl)
|

[![Waffle.io](https://badge.waffle.io/m-lab/etl.svg?title=Ready)](http://waffle.io/m-lab/etl)

MeasurementLab data ingestion pipeline.

To create e.g., NDT table (should rarely be required!!!):
bq mk --time_partitioning_type=DAY --schema=schema/repeated.json mlab-sandbox:mlab_sandbox.ndt

Also see schema/README.md.
