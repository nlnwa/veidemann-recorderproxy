#!/usr/bin/env bash

DOCKER_TAG=latest

if [ -n "$TRAVIS_TAG" -o "$TRAVIS_BRANCH" == "master" -a "$TRAVIS_EVENT_TYPE" == "push" ]; then
  echo "$DOCKER_PASSWORD" | docker login -u "$DOCKER_USERNAME" --password-stdin;

  if [ -n "${TRAVIS_TAG}" ]; then
    DOCKER_TAG=${TRAVIS_TAG}
  fi

  docker push norsknettarkiv/veidemann-recorderproxy:${DOCKER_TAG}
fi
