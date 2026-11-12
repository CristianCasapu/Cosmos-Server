import { Button, useMediaQuery, IconButton, Tooltip } from "@mui/material";


const ResponsiveButton = ({ children, startIcon, endIcon, size, style, ...props }) => {
  const isMobile = useMediaQuery((theme) => theme.breakpoints.down('sm'));
  let newStyle = style || {};
  if (isMobile) {
    newStyle.minHeight = '40px';
    newStyle.fontSize = '145%'
  }

  // Fork addition: below `sm` only the icon is shown, so the hidden text becomes the tooltip.
  // Not when disabled: a disabled button fires no events and a wrapper would change the layout.
  const tooltip = (isMobile && (startIcon || endIcon) && typeof children === 'string' && !props.disabled) ? children : '';

  const button = (
    <Button 
      className="responsive-button"
      size={isMobile ? 'large' : size}
      startIcon={isMobile ? null : startIcon}
      endIcon={isMobile ? null : endIcon}
      {...props} style={newStyle}>
      {(isMobile) ? startIcon : (
        startIcon ? children : null
      )}
      {(isMobile) ? endIcon : (
        endIcon ? children : null
      )}
      {!startIcon && !endIcon ? children : null}
    </Button>
  );

  return tooltip ? <Tooltip title={tooltip}>{button}</Tooltip> : button;
}

export default ResponsiveButton;