// Fork addition (plan 48): the settings of what Cosmos watches by itself
// (src/watch: failing disks, disks filling fast, Docker, snapshots, sign-ins
// from a new country), shown in the "Telegram messages" card and saved as
// config.Watch. The server checks them again (watch.Validate). Plain
// functions: see watchSettings.test.mjs.

export const DEFAULT_FILL_DAYS = 3;
export const MAX_FILL_DAYS = 30;
export const MAX_LATE_HOURS = 24 * 90;

const text = (value) => String(value === undefined || value === null ? '' : value).trim();

export const watchInitialValues = (config) => {
  const w = (config && config.Watch) || {};
  return {
    Watch_SMART: !w.SMARTOff,
    Watch_Fill: !w.FillOff,
    Watch_FillDays: w.FillDays >= 1 && w.FillDays <= MAX_FILL_DAYS ? String(w.FillDays) : String(DEFAULT_FILL_DAYS),
    Watch_Docker: !w.DockerOff,
    Watch_Snapshots: !w.SnapshotsOff,
    Watch_SnapshotLateHours: w.SnapshotLateHours > 0 ? String(w.SnapshotLateHours) : '',
    Watch_Country: !w.CountryOff,
  };
};

export const watchToConfig = (values) => {
  const days = Number(text(values.Watch_FillDays));
  const late = Number(text(values.Watch_SnapshotLateHours));
  return {
    SMARTOff: !values.Watch_SMART,
    FillOff: !values.Watch_Fill,
    FillDays: Number.isInteger(days) && days !== DEFAULT_FILL_DAYS ? days : 0,
    DockerOff: !values.Watch_Docker,
    SnapshotsOff: !values.Watch_Snapshots,
    SnapshotLateHours: Number.isInteger(late) && late > 0 ? late : 0,
    CountryOff: !values.Watch_Country,
  };
};

// '' when fine, else what is wrong: 'days' or 'hours'.
export const watchProblem = (values) => {
  const days = text(values.Watch_FillDays);
  if (!/^[0-9]{1,2}$/.test(days) || Number(days) < 1 || Number(days) > MAX_FILL_DAYS) return 'days';
  const late = text(values.Watch_SnapshotLateHours);
  if (late !== '' && (!/^[0-9]{1,4}$/.test(late) || Number(late) < 1 || Number(late) > MAX_LATE_HOURS)) return 'hours';
  return '';
};
