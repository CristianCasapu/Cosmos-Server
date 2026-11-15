import * as React from 'react';
import * as API from '../../api';
import MainCard from '../../components/MainCard';
import {
  Button,
  Grid,
  InputLabel,
  Skeleton,
  Stack,
  Typography,
} from '@mui/material';
import { CosmosCheckbox } from './users/formShortcuts';
import UploadButtons from '../../components/fileUpload';
import { SliderPicker } from 'react-color';
import { SetPrimaryColor, SetSecondaryColor } from '../../App';
import { useTranslation } from 'react-i18next';

const ConfigAppearance = ({ formik }) => {
  const { t } = useTranslation();
  const [uploadingBackground, setUploadingBackground] = React.useState(false);

  return (
    <MainCard title={t('mgmt.config.appearanceTitle')}>
      <Grid container spacing={3}>
        <Grid size={{ xs: 12 }}>
          {!uploadingBackground && formik.values.Background && <img src=
            {formik.values.Background} alt={t('mgmt.config.appearance.uploadWallpaperButton.previewBrokenError')}
            width={285} />}
          {uploadingBackground && <Skeleton variant="rectangular" width={285} height={140} />}
           <Stack spacing={1} direction="row" useFlexGap sx={{ flexWrap: 'wrap', mt: 1 }}>{/* Fork addition: wraps on a phone */}
            <UploadButtons
              accept='.jpg, .png, .gif, .jpeg, .webp, .bmp, .avif, .tiff, .svg'
              label={t('mgmt.config.appearance.uploadWallpaperButton.uploadWallpaperLabel')}
              OnChange={(e) => {
                setUploadingBackground(true);
                const file = e.target.files[0];
                API.uploadImage(file, "background").then((data) => {
                  formik.setFieldValue('Background', data.data.path);
                  setUploadingBackground(false);
                });
              }}
            />

            <Button
              variant="outlined"
              onClick={() => {
                formik.setFieldValue('Background', "");
              }}
            >
              {t('mgmt.config.appearance.resetWallpaperButton.resetWallpaperLabel')}
            </Button>
            <Button
              variant="outlined"
              onClick={() => {
                formik.setFieldValue('PrimaryColor', "");
                SetPrimaryColor("");
                formik.setFieldValue('SecondaryColor', "");
                SetSecondaryColor("");
              }}
            >
              {t('mgmt.config.appearance.resetColorsButton.resetColorsLabel')}
            </Button>
          </Stack>
        </Grid>

        <Grid size={{ xs: 12 }}>
          <CosmosCheckbox
            label={t('mgmt.config.appearance.appDetailsOnHomepageCheckbox.appDetailsOnHomepageLabel')}
            name="Expanded"
            formik={formik}
          />
        </Grid>

        {/* Fork addition: what the two colours change */}
        <Grid size={{ xs: 12 }}>
          <Typography variant="body2" color="textSecondary" className="ct-theme-note">
            {t('mgmt.config.appearance.colorsNote', 'The two colours tint the whole panel: buttons, links, the selected menu entry, tabs and the keyboard focus ring, in light and dark mode, and the sign-in pages of devices that have opened the panel since. Where a colour would be hard to read, Cosmos uses a lighter or darker shade of it. The changes show at once; Save keeps them.')}
          </Typography>
        </Grid>

        <Grid size={{ xs: 12 }}>
          <Stack spacing={1}>
            <InputLabel style={{marginBottom: '10px'}} htmlFor="PrimaryColor">{t('mgmt.config.appearance.primaryColorSlider')}</InputLabel>
            <SliderPicker
              id="PrimaryColor"
              color={formik.values.PrimaryColor}
              onChange={color => {
                let colorRGB = `rgba(${color.rgb.r}, ${color.rgb.g}, ${color.rgb.b}, ${color.rgb.a})`
                formik.setFieldValue('PrimaryColor', colorRGB);
                SetPrimaryColor(colorRGB);
              }}
            />
          </Stack>
        </Grid>

        <Grid size={{ xs: 12 }}>
          <Stack spacing={1}>
            <InputLabel style={{marginBottom: '10px'}} htmlFor="SecondaryColor">{t('mgmt.config.appearance.secondaryColorSlider')}</InputLabel>
            <SliderPicker
              id="SecondaryColor"
              color={formik.values.SecondaryColor}
              onChange={color => {
                let colorRGB = `rgba(${color.rgb.r}, ${color.rgb.g}, ${color.rgb.b}, ${color.rgb.a})`
                formik.setFieldValue('SecondaryColor', colorRGB);
                SetSecondaryColor(colorRGB);
              }}
            />
          </Stack>
        </Grid>
      </Grid>
    </MainCard>
  );
};

export default ConfigAppearance;
