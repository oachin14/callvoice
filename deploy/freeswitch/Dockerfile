# Lab FreeSWITCH: ESL on 8021 (password ClueCon — lab only), Sofia external + BYOC gateways dir.
FROM safarov/freeswitch:latest

COPY conf/autoload_configs/event_socket.conf.xml /etc/freeswitch/autoload_configs/event_socket.conf.xml
COPY conf/sip_profiles/external.xml /etc/freeswitch/sip_profiles/external.xml
# WSS/WebRTC for SIP.js softphone (see NOTES-WSS.md). May overwrite image default internal profile.
COPY conf/sip_profiles/internal.xml /etc/freeswitch/sip_profiles/internal.xml
COPY conf/directory/default/agent.xml.template /etc/freeswitch/directory/default/agent.xml.template
COPY conf/dialplan/public/00_callvoice_inbound.xml /etc/freeswitch/dialplan/public/00_callvoice_inbound.xml
COPY scripts/inbound.lua /usr/share/freeswitch/scripts/inbound.lua
COPY NOTES-WSS.md /etc/freeswitch/NOTES-WSS.md
RUN mkdir -p /etc/freeswitch/gateways /etc/freeswitch/directory/default /etc/freeswitch/tls

EXPOSE 8021 5060/udp 5060/tcp 5061/tcp 5066 5080/udp 5080/tcp 7443 8081 8082
