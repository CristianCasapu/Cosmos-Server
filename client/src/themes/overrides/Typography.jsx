// ==============================|| OVERRIDES - TYPOGRAPHY ||============================== //

export default function Typography() {
    return {
        MuiTypography: {
            // Fork addition: the element each look is written as. The page's name is its h1 (the last
            // breadcrumb); card and section titles drawn as h3/h4/h5 are its h2s, the titles inside them
            // (h6) h3s, and subtitles are not headings. The looks are unchanged; heading levels no longer
            // jump (h1 -> h5) for screen readers.
            defaultProps: {
                variantMapping: {
                    h1: 'h1', h2: 'h2', h3: 'h2', h4: 'h2', h5: 'h2', h6: 'h3',
                    subtitle1: 'p', subtitle2: 'p', body1: 'p', body2: 'p', inherit: 'p'
                }
            },
            styleOverrides: {
                gutterBottom: {
                    marginBottom: 12
                }
            }
        }
    };
}
