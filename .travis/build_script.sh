#!/usr/bin/env bash

DOCKER_TAG=latest

if [ -n "${TRAVIS_TAG}" ]; then
  DOCKER_TAG=${TRAVIS_TAG}
fi

docker build -t norsknettarkiv/veidemann-recorderproxy:${DOCKER_TAG} .
