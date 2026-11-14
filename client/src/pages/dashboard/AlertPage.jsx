import { unansweredError } from '../../utils/serverAnswer'; // Fork addition
import * as React from 'react';
import * as API from '../../api';
import MainCard from '../../components/MainCard';
import { Formik, Field, useFormik, FormikProvider } from 'formik';
import * as Yup from 'yup';
import {
  Alert,
  Button,
  Checkbox,
  FormControlLabel,
  Grid,
  InputLabel,
  OutlinedInput,
  Stack,
  FormHelperText,
  TextField,
  MenuItem,
  Skeleton,
  CircularProgress,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogContentText,
  DialogActions,
  Box,
} from '@mui/material';
import { ExclamationCircleOutlined, InfoCircleOutlined, PlusCircleOutlined, SyncOutlined, WarningOutlined } from '@ant-design/icons';
import PrettyTableView from '../../components/tableView/prettyTableView';
import { DeleteButton } from '../../components/delete';
import { CosmosCheckbox, CosmosFormDivider, CosmosInputText, CosmosSelect } from '../config/users/formShortcuts';
import { MetricPicker } from './MetricsPicker';
import { useTranslation } from 'react-i18next';
import dayjs from 'dayjs';
import { ForkAlertTemplates, ForkAlertUnit, ForkAlertReadBack, forkAlertValidate } from './forkAlertGuide'; // Fork addition
import { keepOnBackdrop } from '../../components/formHelpers'; // Fork addition

const DisplayOperator = (operator) => {
  switch (operator) {
    case 'gt':
      return '>';
    case 'lt':
      return '<';
    case 'eq':
      return '=';
    default:
      return '?';
  }
}

const EditAlertModal = ({ open, onClose, onSave, metrics, names }) => { // Fork addition: metrics, names
  const { t } = useTranslation();

  const AlertValidationSchema = Yup.object().shape({
    name: Yup.string().required(t('global.name.validation')),
    trackingMetric: Yup.string().required(t('navigation.monitoring.alerts.action.edit.trackingMetric.validation')),
    conditionOperator: Yup.string().required(t('navigation.monitoring.alerts.action.edit.conditionOperator.validation')),
    conditionValue: Yup.number().required(t('navigation.monitoring.alerts.action.edit.conditionValue.validation')),
    period: Yup.string().required(t('navigation.monitoring.alerts.action.edit.period.validation')),
  });

  const formik = useFormik({
    initialValues: {
      name: open.Name || t('navigation.monitoring.alerts.newAlertButton'),
      trackingMetric: open.TrackingMetric || '',
      conditionOperator: (open.Condition && open.Condition.Operator) || 'gt',
      conditionValue: (open.Condition && open.Condition.Value) || 0,
      conditionPercent: (open.Condition && open.Condition.Percent) || false,
      period: open.Period || 'latest',
      actions: open.Actions || [],
      throttled: typeof open.Throttled === 'boolean' ? open.Throttled : true,
      severity: open.Severity || 'error',
    },
    validationSchema: AlertValidationSchema,
    // Fork addition: the checks of utils/alertForm.js (a known metric, a unique name, the unit, actions that work)
    validate: forkAlertValidate(t, { metrics, names, original: open.Name }),
    // Fork addition: the dialog stays open until the server answered, and keeps what was entered if it refused
    onSubmit: (values, { setErrors }) => {
      values.actions = values.actions.filter((a) => !a.removed);
      return Promise.resolve(onSave(values, open.Name)).then(() => onClose()).catch((err) => {
        setErrors({ submit: (err && err.message) || String(err) });
      });
    },
  });

  return (
    <Dialog open={open} onClose={keepOnBackdrop(onClose) /* Fork addition: a click beside it lost the alert */} maxWidth="sm" fullWidth>
      <DialogTitle>{t('navigation.monitoring.alerts.action.edit')}</DialogTitle>
      <FormikProvider value={formik}>
      <form onSubmit={formik.handleSubmit}>
        <DialogContent>
          <Stack spacing={2}>
            {!open.Name && <ForkAlertTemplates formik={formik} metrics={metrics} />}{/* Fork addition: ready-made alerts for a new one */}
            <CosmosInputText
              name="name"
              label={t('navigation.monitoring.alerts.alertNameLabel')}
              formik={formik}
              required
            />
            <MetricPicker
              name="trackingMetric"
              label={t('navigation.monitoring.alerts.trackingMetricLabel')}
              formik={formik}
              required
            />
            {/* Fork addition: one below the other on a phone; side by side the value field and the percent checkbox ran out of the dialog */}
            <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2} alignItems={{ xs: 'stretch', sm: 'center' }}>
              <CosmosSelect
                name="conditionOperator"
                label={t('navigation.monitoring.alerts.conditionOperatorLabel')}
                formik={formik}
                options={[
                  ['gt', '>'],
                  ['lt', '<'],
                  ['eq', '='],
                ]}
              >
              </CosmosSelect>
              <CosmosInputText
                name="conditionValue"
                label={t('navigation.monitoring.alerts.conditionValueLabel')}
                formik={formik}
                required
              />
              <CosmosCheckbox
                style={{paddingTop: '20px'}}
                name="conditionPercent"
                label={t('navigation.monitoring.alerts.conditionLabel')}
                formik={formik}
              />
            </Stack>
            <ForkAlertUnit formik={formik} />{/* Fork addition: the unit of the value */}

            <CosmosSelect
              name="period"
              label={t('navigation.monitoring.alerts.periodLabel')}
              formik={formik}
              options={[
                ['latest', t('navigation.monitoring.latest')],
                ['hourly', t('navigation.monitoring.hourly')],
                ['daily', t('navigation.monitoring.daily')],
            ]}></CosmosSelect>

            <CosmosSelect
              name="severity"
              label={t('navigation.monitoring.alerts.action.edit.severitySelection.severityLabel')}
              formik={formik}
              options={[
                ['info', 'Info'],
                ['warn', 'Warning'],
                ['error', 'Error'],
            ]}></CosmosSelect>

            <CosmosCheckbox
              name="throttled"
              label={t('navigation.monitoring.alerts.throttleCheckbox.throttleLabel')}
              formik={formik}
            />

            <CosmosFormDivider title={t('mgmt.monitoring.alerts.actionTriggersTitle')} />
            
            <Stack direction="column" spacing={2}>
              {formik.values.actions
              .map((action, index) => {
                return !action.removed && <>
                  {action.Type === 'stop' && 
                    <Alert severity="info">{t('navigation.monitoring.alerts.actions.stopActionInfo')}</Alert>
                  }
                  {action.Type === 'restart' &&
                    <Alert severity="info">{t('navigation.monitoring.alerts.actions.restartActionInfo')}</Alert>
                  }
                  <Stack direction="row" spacing={2} key={index}>
                    <Box style={{
                      width: '100%',
                      minWidth: 0, // Fork addition: a long action name pushed the delete button out of the dialog on a phone
                    }}>
                      <CosmosSelect
                        name={`actions.${index}.Type`}
                        label={t('navigation.monitoring.alerts.action.edit.actionTypeInput.actionTypeLabel')}
                        formik={formik}
                        options={[
                          ['notification', t('navigation.monitoring.alerts.actions.sendNotification')],
                          ['email', t('navigation.monitoring.alerts.actions.sendEmail')],
                          ['stop', t('navigation.monitoring.alerts.actions.stop')],
                          ['restart', t('navigation.monitoring.alerts.actions.restart')],
                        ]}
                      />
                    </Box>
                    
                    <Box  style={{
                      height: '95px',
                      display: 'flex',
                      alignItems: 'center',
                    }}>
                      <DeleteButton
                        onDelete={() => {
                          formik.setFieldValue(`actions.${index}.removed`, true);
                        }}
                      />
                    </Box>
                  </Stack>
                </>
              })}

              <Button
                variant="outlined"
                color="primary"
                startIcon={<PlusCircleOutlined />}
                onClick={() => {
                  formik.setFieldValue('actions', [
                    ...formik.values.actions,
                    {
                      Type: 'notification',
                    },
                  ]);
                }}>
                {t('mgmt.monitoring.alerts.addActionButton')}
              </Button>
            </Stack>
            <ForkAlertReadBack formik={formik} metrics={metrics} />{/* Fork addition: the alert in one sentence */}
            {formik.errors.submit && <Alert severity="error">{formik.errors.submit}</Alert>}

          </Stack>
        </DialogContent>
        <DialogActions>
          <Button onClick={onClose}>{t('global.cancelAction')}</Button>
          <Button variant='contained' type="submit" disabled={formik.isSubmitting}>{t('global.saveAction')}</Button>
        </DialogActions>
      </form>
      </FormikProvider>
    </Dialog>
  );
};

const AlertPage = () => {
  const { t } = useTranslation();
  const [config, setConfig] = React.useState(null);
  const [isLoading, setIsLoading] = React.useState(false);
  const [openModal, setOpenModal] = React.useState(false);
  const [metrics, setMetrics] = React.useState({});

  // Fork addition: `retries`. Saving makes the server restart its listener, which refuses the
  // reload that follows a change; the list then kept showing the alerts from before the change,
  // with its checkboxes and delete buttons disabled. After a change the reload is tried again,
  // once a second.
  function refresh(retries = 0) {
    let again = false;
    const retry = (error) => {
      // only when the server did not answer at all: an error it answered with has been shown
      if (retries > 0 && !again && unansweredError(error)) { // also when a proxy answered in the server's place (utils/serverAnswer.js)
        again = true;
        setTimeout(() => refresh(retries - 1), 1000);
      }
    };

    API.config.get().then((res) => {
      setConfig(res.data);
      setIsLoading(false);
    }).catch(retry);
    API.metrics.list().then((res) => {
      setMetrics(res.data);
    }).catch(retry);
  }

  React.useEffect(() => {
    refresh();
  }, []);

  const setEnabled = (name) => (event) => {
    setIsLoading(true);
    let toSave = {
      ...config,
      MonitoringAlerts: {
        ...config.MonitoringAlerts,
        [name]: {
          ...config.MonitoringAlerts[name],
          Enabled: event.target.checked,
        }
      }
    };

    API.config.set(toSave).then(() => {
      refresh(15); // fork addition: the server may be restarting its listener
    });
  }

  const deleteAlert = (name) => {
    setIsLoading(true);
    let toSave = {
      ...config,
      MonitoringAlerts: {
        ...config.MonitoringAlerts,
      }
    };
    delete toSave.MonitoringAlerts[name];

    API.config.set(toSave).then(() => {
      refresh(15); // fork addition: the server may be restarting its listener
    });
  }
  
  const saveAlert = (data, original) => { // Fork addition: original, the name the alert had
    setIsLoading(true);
    
    data.conditionValue = parseInt(data.conditionValue);
    data.name = String(data.name).trim(); // Fork addition

    // Fork addition: a renamed alert replaces its old entry instead of leaving a copy of it
    const kept = { ...config.MonitoringAlerts };
    if (original && original !== data.name) delete kept[original];

    let toSave = {
      ...config,
      MonitoringAlerts: {
        ...kept,
        [data.name]: {
          Name: data.name,
          Enabled: true,
          TrackingMetric: data.trackingMetric,
          Condition: {
            Operator: data.conditionOperator,
            Value: data.conditionValue,
            Percent: data.conditionPercent,
          },
          Period: data.period,
          Actions: data.actions,
          LastTriggered: null,
          Throttled: data.throttled,
          Severity: data.severity,
        }
      }
    };

    // Fork addition: returned, so that the dialog waits for the answer
    return API.config.set(toSave).then(() => {
      refresh(15); // fork addition: the server may be restarting its listener
    }).catch((err) => {
      setIsLoading(false);
      throw err;
    });
  }
  
  // Fork addition: the actions below carried the key "mgmt.storage.typeTitle" instead of "Type", so the restored alerts did nothing
  const resetTodefault = () => {
    setIsLoading(true);

    let toSave = {
      ...config,
      DiskAlertsSeeded: [], // Fork addition: the server adds the default alerts of the data disks again
      MonitoringAlerts: {
        "Anti Crypto-Miner": {
          "Name": "Anti Crypto-Miner",
          "Enabled": false,
          "Period": "daily",
          "TrackingMetric": "cosmos.system.docker.cpu.*",
          "Condition": {
            "Operator": "gt",
            "Value": 80
          },
          "Actions": [
            {
              "Type": "notification",
              "Target": ""
            },
            {
              "Type": "email",
              "Target": ""
            },
            {
              "Type": "stop",
              "Target": ""
            }
          ],
          "LastTriggered": "0001-01-01T00:00:00Z",
          "Throttled": false,
          "Severity": "warn"
        },
        "Anti Memory Leak": {
          "Name": "Anti Memory Leak",
          "Enabled": false,
          "Period": "daily",
          "TrackingMetric": "cosmos.system.docker.ram.*",
          "Condition": {
            "Percent": true,
            "Operator": "gt",
            "Value": 80
          },
          "Actions": [
            {
              "Type": "notification",
              "Target": ""
            },
            {
              "Type": "email",
              "Target": ""
            },
            {
              "Type": "stop",
              "Target": ""
            }
          ],
          "LastTriggered": "0001-01-01T00:00:00Z",
          "Throttled": false,
          "Severity": "warn"
        },
        "Disk Full Notification": {
          "Name": 'Disk Full Notification',
          "Enabled": true,
          "Period": "latest",
          "TrackingMetric": "cosmos.system.disk./",
          "Condition": {
            "Percent": true,
            "Operator": "gt",
            "Value": 95
          },
          "Actions": [
            {
              "Type": "notification",
              "Target": ""
            }
          ],
          "LastTriggered": "0001-01-01T00:00:00Z",
          "Throttled": true,
          "Severity": "warn"
        }
      }
    };

    API.config.set(toSave).then(() => {
      refresh(15); // fork addition: the server may be restarting its listener
    });
  }

  const GetSevIcon = ({level}) => {
    switch (level) {
      case 'info':
        return <span style={{color: '#2196f3'}}><InfoCircleOutlined /></span>;
      case 'warn':
        return <span style={{color: '#ff9800'}}><WarningOutlined /></span>;
      case 'error':
        return <span style={{color: '#f44336'}}><ExclamationCircleOutlined /></span>;
      default:
        return '';
    }
  }

  return <div style={{ maxWidth: "1200px", margin: "auto" }}>
    {openModal && <EditAlertModal open={openModal} onClose={() => setOpenModal(false)} onSave={saveAlert}
      metrics={metrics} names={Object.keys((config && config.MonitoringAlerts) || {})} /* Fork addition */ />}

    {/* Fork addition: useFlexGap flexWrap, the buttons go to a second line on a phone instead of being squeezed */}
    <Stack direction="row" spacing={2} useFlexGap flexWrap="wrap" style={{marginBottom: '15px'}}>
      <Button variant="contained" color="primary" startIcon={<SyncOutlined />} onClick={() => {
          refresh();
      }}>{t('global.refresh')}</Button>
      <Button variant="contained" color="primary" startIcon={<PlusCircleOutlined />} onClick={() => {
          setOpenModal(true);
      }}>{t('global.createAction')}</Button>
      <Button variant="outlined" color="warning" startIcon={<WarningOutlined />} onClick={() => {
        resetTodefault();
      }}>{t('navigation.monitoring.alerts.resetToDefaultButton')}</Button>
    </Stack>
    
    {config && <>
      <Formik
        initialValues={{
          Actions: config.MonitoringAlerts
        }}

        // validationSchema={Yup.object().shape({
        // })}

        onSubmit={async (values, { setErrors, setStatus, setSubmitting }) => {
          setSubmitting(true);
        
          let toSave = {
            ...config,
            MonitoringAlerts: values.Actions
          };

          return API.config.set(toSave);
        }}
      >
        {(formik) => (
          <form noValidate onSubmit={formik.handleSubmit}>
            <Stack spacing={3}>
            {!config && <Skeleton variant="rectangular" height={300} />}
            {config && (!config.MonitoringAlerts || !Object.values(config.MonitoringAlerts).length) ? <Alert severity="info">No alerts configured.</Alert> : ''}
            {config && config.MonitoringAlerts && Object.values(config.MonitoringAlerts).length ? <PrettyTableView 
              data={Object.values(config.MonitoringAlerts)}
              getKey={(r) => r.Name + r.Target + r.Mode}
              onRowClick={(r, k) => {
                setOpenModal(r);
              }}

              columns={[
                { 
                  title: t('global.enabled'), 
                  clickable:true, 
                  field: (r, k) => <Checkbox disabled={isLoading} size='large' color={r.Enabled ? 'success' : 'default'}
                    onChange={setEnabled(Object.keys(config.MonitoringAlerts)[k])}
                    checked={r.Enabled}
                  />,
                  style: {
                  },
                },
                { 
                  title: t('global.nameTitle'), 
                  field: (r) => <><GetSevIcon level={r.Severity} /> {r.Name}</>,
                },
                { 
                  title: t('navigation.monitoring.alerts.trackingMetricTitle'), 
                  field: (r) => metrics[r.TrackingMetric] ? metrics[r.TrackingMetric] : r.TrackingMetric,
                },
                { 
                  title: t('navigation.monitoring.alerts.conditionTitle'), 
                  screenMin: 'md',
                  field: (r) => DisplayOperator(r.Condition.Operator) + ' ' + r.Condition.Value + (r.Condition.Percent ? '%' : ''),
                },
                { 
                  title: t('navigation.monitoring.alerts.periodTitle'),
                  field: (r) => t(r.Period),
                },
                { 
                  title: t('navigation.monitoring.alerts.astTriggeredTitle'),
                  screenMin: 'md',
                  field: (r) => (r.LastTriggered != "0001-01-01T00:00:00Z") ? dayjs(new Date(r.LastTriggered)).format('L, LT') : t('global.never'),
                },
                { 
                  title: t('navigation.monitoring.alerts.actionsTitle'), 
                  field: (r) => r.Actions.map((a) => a.Type).join(', '),
                  screenMin: 'md',
                },
                { title: '', clickable:true, field: (r, k) =>  <DeleteButton disabled={isLoading} onDelete={() => {
                  deleteAlert(Object.keys(config.MonitoringAlerts)[k])
                }}/>,
                  style: {
                    textAlign: 'right',
                  }
                },
              ]}
            /> : ''}
            </Stack>
          </form>
        )}
      </Formik>
    </>}

  </div>;
}

export default AlertPage;