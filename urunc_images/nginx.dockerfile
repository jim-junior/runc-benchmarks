FROM harbor.nbfc.io/nubificus/urunit:latest AS init

FROM nginx:alpine

COPY --from=init /urunit /urunit

COPY run-nginx.sh /run-nginx.sh

RUN chmod +x /run-nginx.sh