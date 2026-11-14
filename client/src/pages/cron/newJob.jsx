import React from "react";
import { useEffect, useState } from "react";
import * as API  from "../../api";
import PrettyTableView from "../../components/tableView/prettyTableView";
import { DeleteButton } from "../../components/delete";
import { CheckCircleOutlined, CloudOutlined, CompassOutlined, DesktopOutlined, ExpandOutlined, InfoCircleOutlined, LaptopOutlined, MenuFoldOutlined, MenuOutlined, MinusCircleFilled, MobileOutlined, NodeCollapseOutlined, PlusCircleFilled, PlusCircleOutlined, ReloadOutlined, SettingFilled, TabletOutlined, WarningFilled, WarningOutlined } from "@ant-design/icons";
import { Alert, Button, CircularProgress, Dialog, DialogActions, DialogContent, DialogContentText, DialogTitle, InputLabel, LinearProgress, ListItemIcon, ListItemText, MenuItem, Stack, TextField, Tooltip } from "@mui/material";
import { CosmosCheckbox, CosmosFormDivider, CosmosInputText } from "../config/users/formShortcuts";
import MainCard from "../../components/MainCard";
import { Formik, FormikProvider, useFormik } from "formik";
import { LoadingButton, TreeItem, TreeView } from "@mui/lab";
import ApiModal from "../../components/apiModal";
import ConfirmModal from "../../components/confirmModal";
import { PascalToSnake, crontabToText, isDomain } from "../../utils/indexs";
import UploadButtons from "../../components/fileUpload";
import { useTheme } from '@mui/material/styles';
import MiniPlotComponent from '../dashboard/components/mini-plot';
import LogLine from "../../components/logLine";
import { CosmosContainerPicker } from '../config/users/containerPicker';
import * as yup from "yup";
import { useTranslation } from 'react-i18next';
import CronHelp, { cronFieldErrors } from '../../components/cronInput'; // Fork addition: the schedule in words, checked, with schedules to pick
import { jobCommandProblem, jobNameProblem, jobRunsIn } from '../../utils/cronJobForm'; // Fork addition: name and command checked
import { keepOnBackdrop } from '../../components/formHelpers'; // Fork addition
import { serverBack } from '../../utils/appLinks'; // Fork addition

const NewJobDialog = ({job, OnClose, refresh}) => {
  const { t } = useTranslation();
  const isEdit = job && typeof job === 'object';
  const [config, setConfig] = useState(null);

  const formik = useFormik({
    initialValues: {
      Enabled: isEdit ? job.Enabled : true,
      Name: isEdit ? job.Name : '',
      Crontab: isEdit ? job.Crontab : '',
      Command: isEdit ? job.Command : '',
      Container: isEdit ? job.Container : '',
    },
    validateOnChange: false,
    // Fork addition: a schedule the server would not run is not saved; nor is a job without a name or with nothing to run
    validate: (values) => ({
      ...cronFieldErrors(values, ['Crontab']),
      ...(jobNameProblem(values.Name) ? { Name: t('mgmt.cron.fork.nameRequired', 'Give the job a name, for example "Back up the photos"') } : {}),
      ...(jobCommandProblem(values.Command) ? { Command: t('mgmt.cron.fork.commandRequired', 'Enter the command to run, for example ls -la /storage') } : {}),
    }),
    validationSchema: yup.object({
    }),
    onSubmit: async (values, { setErrors, setStatus, setSubmitting }) => {
      setSubmitting(true);
      let configFile = (await API.config.get()).data;
      if(!configFile.CRON) {
        configFile.CRON = {};
      }

      // Fork addition: a job may not take the name of another one, which it would replace without a word
      if (jobNameProblem(values.Name, Object.keys(configFile.CRON), isEdit ? job.Name : undefined) === 'taken') {
        setErrors({ Name: t('mgmt.cron.fork.nameTaken', 'Another job is called "{{name}}": pick another name, or edit that job', { name: values.Name.trim() }) });
        setSubmitting(false);
        return;
      }
      values = { ...values, Name: values.Name.trim() };

      if(isEdit) {
        delete configFile.CRON[job.Name];
      }

      configFile.CRON[values.Name] = values;

      return API.config.set(configFile).then((res) => {
        setStatus({ success: true });
        setSubmitting(false);
        // Fork addition: the save makes the server restart its listener; asked at once, the list of
        // jobs was refused and kept showing the jobs from before the save
        serverBack().then(() => refresh && refresh());
        OnClose && OnClose();
      }).catch((err) => {
        setStatus({ success: false });
        setErrors({ submit: err.message });
        setSubmitting(false);
        refresh && refresh();
      });
    },
  });
  
  useEffect(() => {
    if (isEdit && job) {
      API.config.get().then((res) => {
        setConfig(res.data);
        let configJob = res.data.CRON[job.Name];
        if (!configJob) {
          return;
        }
        formik.setValues(configJob);
      });
    }
  }, [job]);
  

  // Fork addition: a click beside the form does not close it
  return <Dialog open={job} onClose={keepOnBackdrop(() => {
      OnClose && OnClose();
    })}>
      <FormikProvider value={formik}>
        <form onSubmit={formik.handleSubmit}>
          {/* Fork addition: a new job is not an edit */}
          <DialogTitle>{isEdit ? t('mgmt.cron.editCronTitle') : t('mgmt.cron.newCronTitle')}</DialogTitle>
          <DialogContent>
              <DialogContentText>
                <Stack spacing={2} style={{ marginTop: '10px', width: '500px', maxWidth: '100%' }}>
                  <div>
                    {t('mgmt.cron.editCron.customText')} (<strong>{t('mgmt.cron.editCron.customText.onHostOnly')}</strong>).
                  </div>
                  <TextField
                    fullWidth
                    id="Name"
                    name="Name"
                    label={t('mgmt.cron.newCron.cronNameInput.cronNameLabel')}
                    value={formik.values.Name}
                    onChange={formik.handleChange}
                    error={formik.touched.Name && Boolean(formik.errors.Name)}
                    helperText={formik.touched.Name && formik.errors.Name}
                  />
                  
                  <TextField
                    fullWidth
                    id="Crontab"
                    name="Crontab"
                    label={t('mgmt.cron.newCron.crontabInput.crontabLabel')}
                    value={formik.values.Crontab}
                    onChange={formik.handleChange}
                    error={formik.touched.Crontab && Boolean(formik.errors.Crontab)}
                    helperText={formik.touched.Crontab && formik.errors.Crontab}
                  />
                  {/* Fork addition: replaces the line that put the schedule in words */}
                  <CronHelp formik={formik} name="Crontab" />

                  <TextField
                    fullWidth
                    id="Command"
                    name="Command"
                    label={t('mgmt.cron.newCron.commandInput.commandLabel')}
                    value={formik.values.Command}
                    onChange={formik.handleChange}
                    error={formik.touched.Command && Boolean(formik.errors.Command)}
                    helperText={(formik.touched.Command && formik.errors.Command) || /* Fork addition: where it runs */ (jobRunsIn(formik.values.Container) === 'container'
                      ? t('mgmt.cron.fork.runsInContainer', 'Runs inside the container {{name}}, which has to be running at that time.', { name: formik.values.Container })
                      : t('mgmt.cron.fork.runsOnServer', 'Runs on the server itself, with the rights of Cosmos. Pick a container below to run it inside an app instead.'))}
                  />

                  <CosmosContainerPicker
                    formik={formik}
                    onTargetChange={(_, name) => {
                      formik.setFieldValue('Container', name);
                    }}
                    name='Container'
                    nameOnly
                  />
                   
                  {formik.errors.submit && <Alert severity="error">{formik.errors.submit}</Alert>}
                </Stack>
              </DialogContentText>
          </DialogContent>
          <DialogActions>
              <Button onClick={() => {
                  OnClose && OnClose();
              }}>{t('global.close')}</Button>
              {/* Fork addition: the button submits the form once (it also called handleSubmit itself, so every job was saved twice) */}
              <LoadingButton color="primary" variant="contained" type="submit" loading={formik.isSubmitting}>{t('mgmt.cron.newCron.submitButton')}</LoadingButton>
          </DialogActions>
        </form>
      </FormikProvider>
  </Dialog>
};

export default NewJobDialog;