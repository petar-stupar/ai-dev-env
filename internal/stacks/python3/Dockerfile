# Python 3 from Ubuntu; pip downloads go to the cache volume so they survive container recreation.
RUN apt-get update \
 && apt-get install -y --no-install-recommends python3 python3-pip python3-venv python3-dev \
 && rm -rf /var/lib/apt/lists/*
ENV PIP_CACHE_DIR=/var/cache/aide/pip
