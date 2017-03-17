FROM scratch
COPY bin/etcdcd /etcdcd
COPY ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ENTRYPOINT ["/etcdcd"]
