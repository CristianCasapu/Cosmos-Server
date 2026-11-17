// Fork addition: what the scheduled-job dialog checks besides the schedule
// (utils/cronExpression.js). Upstream saved a job without a name under the
// name "", let a job take the name of another and replace it without a word
// (creating, and renaming too), and saved a job with nothing to run.
// Nothing here imports the UI.

// Why a job cannot be saved under this name, or null:
//   'required' | 'long' (over 64 characters) | 'chars' (a line break, an
//   invisible character, < or >) | 'taken' (another job has it; `editing` is the name the job
//   being edited had, which it may keep)
export const jobNameProblem = (name, existing, editing) => {
  const text = String(name || '').trim();
  if (!text) {
    return 'required';
  }
  // as the server: at most 64 characters, one line, no < or > (src/cron/fork_job_check.go)
  if ([...text].length > 64) {
    return 'long';
  }
  if (/[\u0000-\u001f\u007f-\u009f<>]/.test(text)) {
    return 'chars';
  }
  if (text !== editing && (existing || []).includes(text)) {
    return 'taken';
  }
  return null;
};

// 'required' for a job with nothing to run, else null.
export const jobCommandProblem = (command) => (String(command || '').trim() ? null : 'required');

// Where the command runs: 'container' when one is picked, else 'server'.
export const jobRunsIn = (container) => (String(container || '').trim() ? 'container' : 'server');
