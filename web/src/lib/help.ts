// All of Local Lid's in-page help text, in one place, so wording (and any
// later renames, e.g. "Dials" → "Local ports") changes here only.
// Terms follow the datumctl connect CLI where it has one.

/** Shown when hovering a sidebar tab. */
export const TAB_HELP = {
  services: 'Apps on this device shared with other devices using datumctl connect serve',
  dials: 'Local ports on this device that reach apps on other devices, opened using datumctl connect dial',
  networks: 'Networks this device has joined through a Datum gateway using datumctl connect join',
  logs: 'Log files on this device registered using local-lid logs add',
} as const;

/** Shown under section headings. */
export const SECTION_HELP = {
  // Service page
  sharing: 'Where other devices reach this app, who can connect, and whether it’s up',
  allowedPeers: 'Devices allowed to connect, by public key',
  capturedTraffic: 'HTTP requests seen by Local Lid’s inspector, newest first. Click one for details or to replay it',
  connections: 'Who’s connected right now. The Connect daemon doesn’t report this yet',

  // Dial page
  dialConnection: 'Direct (peer to peer) or relay (through a Datum relay when direct is blocked), and the round-trip time',
  dialDetails: 'Point your app at the local address to reach the app on the other device',

  // Network page
  networkPackets: 'Packets sent and received through this network',
  networkAttachment: 'This device’s address in the network, the address ranges it can reach, and its gateway',

  // Device page
  project: 'Whether this device is enrolled and connected to the project',
  traffic: 'Totals through the Connect daemon on this device since it started',
  connector: 'This device’s identity in Datum and the login it uses',
  localLid: 'This dashboard: where it runs, which daemon it reads, and whether replay is on',
} as const;

export const MAP_HELP =
  'Apps this device shares are on the left; local ports and networks it reaches are on the right. Colour shows health. Click a station to open it, or hover for its full note.';

export const FOOTER_HELP = {
  map: 'Everything this device shares and reaches, as a map',
  device: 'This device, its connection to the project, and traffic totals',
} as const;
