# Use the official Eclipse Mosquitto image as the base
FROM eclipse-mosquitto:latest

# Set the maintainer label
LABEL maintainer="johnscode <iotagg@johnscode.com>"

# Expose MQTT port (non-TLS, jika masih digunakan)
EXPOSE 1883

# Expose MQTTS (MQTT over TLS) port
EXPOSE 8883

# Copy custom configuration file
COPY mosquitto.conf /mosquitto/config/mosquitto.conf

# Copy TLS certificates
COPY certs/ /mosquitto/config/certs/

# Copy password file
COPY mosquitto_credentials/passwdfile /mosquitto/config/passwdfile
# Set the entrypoint to run Mosquitto
ENTRYPOINT ["/usr/sbin/mosquitto", "-c", "/mosquitto/config/mosquitto.conf"]