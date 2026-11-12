import { LeftOutlined } from "@ant-design/icons";
import { IconButton, Tooltip } from "@mui/material";
import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";

function Back() {
	const navigate = useNavigate();
	const { t } = useTranslation();
	const goBack = () => {
		navigate(-1);
	}
	// Fork addition: tooltip on the icon-only button
	return <Tooltip title={t('global.backAction', 'Back')}><IconButton onClick={goBack}>
		<LeftOutlined />
	</IconButton></Tooltip>	
;
}

export default Back;