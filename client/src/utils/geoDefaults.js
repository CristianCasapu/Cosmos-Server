// Fork addition: the country list "Reset to default" proposes for blocking,
// without the country the admin or the server is in.
//
// Upstream's default list (the countries most scans come from) holds sixteen
// countries, one press of a button. An admin who lives in one of them, or
// whose server stands in one, blocked every visitor from home that way, and
// with the panel reachable from the internet, themselves. The country is
// worked out from what is at hand without asking anyone: the server's
// country setting, the time zones of the browser and the server, and the
// regions of the browser's languages. Those are taken off the list, and the
// form says so.

export const DEFAULT_BLOCKED = ['CN', 'RU', 'TR', 'BR', 'BD', 'IN', 'NP', 'PK', 'LK', 'VN', 'ID', 'IR', 'IQ', 'EG', 'AF', 'RO'];

// The time zones of the countries on the default list (only those matter).
const ZONES = {
  RO: ['Europe/Bucharest'],
  TR: ['Europe/Istanbul', 'Asia/Istanbul'],
  CN: ['Asia/Shanghai', 'Asia/Urumqi', 'Asia/Chongqing', 'Asia/Harbin', 'Asia/Kashgar', 'PRC'],
  IN: ['Asia/Kolkata', 'Asia/Calcutta'],
  BD: ['Asia/Dhaka', 'Asia/Dacca'],
  NP: ['Asia/Kathmandu', 'Asia/Katmandu'],
  PK: ['Asia/Karachi'],
  LK: ['Asia/Colombo'],
  VN: ['Asia/Ho_Chi_Minh', 'Asia/Saigon'],
  ID: ['Asia/Jakarta', 'Asia/Makassar', 'Asia/Jayapura', 'Asia/Pontianak', 'Asia/Ujung_Pandang'],
  IR: ['Asia/Tehran', 'Iran'],
  IQ: ['Asia/Baghdad'],
  EG: ['Africa/Cairo', 'Egypt'],
  AF: ['Asia/Kabul'],
  RU: ['Europe/Moscow', 'Europe/Kaliningrad', 'Europe/Samara', 'Europe/Volgograd', 'Europe/Saratov', 'Europe/Ulyanovsk',
    'Europe/Astrakhan', 'Europe/Kirov', 'Asia/Yekaterinburg', 'Asia/Omsk', 'Asia/Novosibirsk', 'Asia/Barnaul', 'Asia/Tomsk',
    'Asia/Novokuznetsk', 'Asia/Krasnoyarsk', 'Asia/Irkutsk', 'Asia/Chita', 'Asia/Yakutsk', 'Asia/Khandyga', 'Asia/Vladivostok',
    'Asia/Ust-Nera', 'Asia/Magadan', 'Asia/Sakhalin', 'Asia/Srednekolymsk', 'Asia/Kamchatka', 'Asia/Anadyr', 'W-SU'],
  BR: ['America/Sao_Paulo', 'America/Noronha', 'America/Belem', 'America/Fortaleza', 'America/Recife', 'America/Araguaina',
    'America/Maceio', 'America/Bahia', 'America/Campo_Grande', 'America/Cuiaba', 'America/Santarem', 'America/Porto_Velho',
    'America/Boa_Vista', 'America/Manaus', 'America/Eirunepe', 'America/Rio_Branco', 'Brazil/East', 'Brazil/West'],
};

const countryOfZone = (zone) => {
  const name = String(zone || '').trim();
  return Object.keys(ZONES).find((country) => ZONES[country].includes(name)) || '';
};

// The region a language tag names or implies: "ro" and "ro-RO" give RO.
const regionOfLanguage = (tag) => {
  try {
    return (new Intl.Locale(String(tag)).maximize().region || '').toUpperCase();
  } catch (e) {
    return '';
  }
};

// The countries the admin or the server may be in, as two-letter codes.
// `serverCountry` is the setting, `zones` time zone names (browser, server),
// `languages` the browser's language tags.
export const ownCountries = ({ serverCountry, zones, languages } = {}) => {
  const found = new Set();
  const code = String(serverCountry || '').trim().toUpperCase();
  if (/^[A-Z]{2}$/.test(code)) {
    found.add(code);
  }
  (zones || []).map(countryOfZone).filter(Boolean).forEach((country) => found.add(country));
  (languages || []).map(regionOfLanguage).filter((region) => /^[A-Z]{2}$/.test(region)).forEach((region) => found.add(region));
  return [...found];
};

// The default list without the countries in `own`, and which were left out.
export const safeDefaultBlocked = (own) => {
  const mine = new Set((own || []).map((country) => String(country).toUpperCase()));
  return {
    list: DEFAULT_BLOCKED.filter((country) => !mine.has(country)),
    leftOut: DEFAULT_BLOCKED.filter((country) => mine.has(country)),
  };
};
