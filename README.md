# hncheck [![Build Status](https://travis-ci.org/brandur/hncheck.svg?branch=master)](https://travis-ci.org/brandur/hncheck)

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

[mailgun]: https://mailgun.com
