# Lab FreeSWITCH: ESL on 8021 (password ClueCon — lab only), Sofia external + BYOC gateways dir.
FROM safarov/freeswitch:latest

COPY conf/autoload_configs/event_socket.conf.xml /etc/freeswitch/autoload_configs/event_socket.conf.xml
COPY conf/sip_profiles/external.xml /etc/freeswitch/sip_profiles/external.xml
RUN mkdir -p /etc/freeswitch/gateways

EXPOSE 8021 5060/udp 5060/tcp 5080/udp 5080/tcp 8081 8082
