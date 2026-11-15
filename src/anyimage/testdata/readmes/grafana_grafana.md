# Grafana Docker image

## Run the Grafana Docker container

Start the Docker container by binding Grafana to external port `3000`.

```bash
docker run -d --name=grafana -p 3000:3000 grafana/grafana
```

Try it out, default admin user credentials are admin/admin.

Further documentation on how to run Docker can be found at http://docs.grafana.org/installation/docker/.

## Changelog

You can find information about Grafana in our [docs](https://grafana.com/docs/), or find out what's new in the latest release [here](https://grafana.com/docs/grafana/latest/whatsnew/)!