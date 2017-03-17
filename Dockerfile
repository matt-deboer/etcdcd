FROM scratch
COPY bin/etcdcd /etcdcd
ADD https://curl.haxx.se/ca/cacert-2017-01-18.pem /etc/ssl/certs/ca-certificates.crt
ENTRYPOINT /etcdcd
