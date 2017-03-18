# hncheck

A very simple app that checks to see if something under one
of your domains has been submitted to HN, and emails you if
it has.

## Setup

``` sh
cp .env.sample .env
# edit .env

go build
forego run ./hncheck
```
