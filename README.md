# hncheck [![Build Status](https://github.com/brandur/hncheck/workflows/hncheck%20CI/badge.svg)](https://github.com/brandur/hncheck/actions)

A very simple app that checks to see if something under one
of your domains has been submitted to HN, and emails you if
it has.

## Setup

You'll need some SMTP credentials for the app to be able to
send you email. I recommend getting a [free account over at
Mailgun][mailgun] (note this will have to be activated and
you'll have to add yourself as an authorized recipient).

``` sh
cp .env.sample .env
# edit .env

go install
forego start hncheck
```

## Tests

Run the test suite:

``` sh
go test
```

[mailgun]: https://mailgun.com
