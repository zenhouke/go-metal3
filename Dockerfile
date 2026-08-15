FROM golang:1.25.0-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w -buildid=' -o /out/go-metal3-api ./cmd/go-metal3-api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/go-metal3-api /usr/local/bin/go-metal3-api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/go-metal3-api"]
