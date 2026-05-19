package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

type ModernTheme struct{}

func (m ModernTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if variant == theme.VariantDark {
		switch name {
		case theme.ColorNameBackground:
			return color.NRGBA{R: 11, G: 15, B: 25, A: 255} // Sleek deep space slate-950
		case theme.ColorNameInputBackground:
			return color.NRGBA{R: 22, G: 28, B: 44, A: 255} // Sleek card background slate-900
		case theme.ColorNamePrimary:
			return color.NRGBA{R: 99, G: 102, B: 241, A: 255} // Vibrant Violet/Indigo (Indigo 500)
		case theme.ColorNameButton:
			return color.NRGBA{R: 31, G: 41, B: 55, A: 255} // Modern grey-slate button (Slate 800)
		case theme.ColorNameHover:
			return color.NRGBA{R: 139, G: 92, B: 246, A: 80} // Soft hover glow (Violet)
		case theme.ColorNameForeground:
			return color.NRGBA{R: 243, G: 244, B: 246, A: 255} // Cool white text
		case theme.ColorNamePlaceHolder:
			return color.NRGBA{R: 148, G: 163, B: 184, A: 255} // Slate-400 placeholder
		case theme.ColorNameScrollBar:
			return color.NRGBA{R: 71, G: 85, B: 105, A: 120} // Translucent scrollbar
		}
	} else {
		// Gorgeous Light Variant
		switch name {
		case theme.ColorNameBackground:
			return color.NRGBA{R: 248, G: 250, B: 252, A: 255} // Clean cool ice-blue slate-50
		case theme.ColorNameInputBackground:
			return color.NRGBA{R: 255, G: 255, B: 255, A: 255} // Pure white
		case theme.ColorNamePrimary:
			return color.NRGBA{R: 79, G: 70, B: 229, A: 255} // Rich royal indigo-600
		case theme.ColorNameButton:
			return color.NRGBA{R: 241, G: 245, B: 249, A: 255} // Slate-100 button
		case theme.ColorNameHover:
			return color.NRGBA{R: 79, G: 70, B: 229, A: 25} // Very soft blue hover glow
		case theme.ColorNameForeground:
			return color.NRGBA{R: 15, G: 23, B: 42, A: 255} // Deep slate-900 text
		case theme.ColorNamePlaceHolder:
			return color.NRGBA{R: 100, G: 116, B: 139, A: 255} // Slate-500 placeholder
		}
	}
	return theme.DefaultTheme().Color(name, variant)
}

func (m ModernTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (m ModernTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (m ModernTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 8
	case theme.SizeNameScrollBar:
		return 10
	case theme.SizeNameText:
		return 14
	case theme.SizeNameHeadingText:
		return 20
	case theme.SizeNameInputBorder:
		return 2
	}
	return theme.DefaultTheme().Size(name)
}
