; agent-keepalive setup for Windows (NSIS 3, built on Linux by build-windows.sh).
;
; Per-user: installs to %LOCALAPPDATA%\Programs\agent-keepalive, writes only to
; HKCU, never asks for administrator rights.
;
;   setup.exe                interactive
;   setup.exe /S             silent install (keeps the current start-at-login choice)
;   setup.exe /S /UPDATE     what the self-updater runs: stop the running server
;                            and window, replace the files, open the window again
;   uninstall.exe /S         silent uninstall
;
; Build (build-windows.sh passes these):
;   makensis -DVERSION=1.2.0 -DFILEVERSION=1.2.0.0 -DARCH=x64|arm64
;            -DBINARY=<path to agent-keepalive.exe> -DOUTFILE=<setup.exe>
;            -DICON=<.ico> -DLICENSEFILE=<LICENSE> [-DDESKTOP=<agent-keepalive-desktop.exe>]
;            [-DSIGNCMD=<command>] installer.nsi
;
; Without DESKTOP the setup has no desktop window, and `app` opens a browser.

Unicode true
ManifestDPIAware true
RequestExecutionLevel user
SetCompressor /SOLID lzma

!include "MUI2.nsh"
!include "FileFunc.nsh"
!include "LogicLib.nsh"
!include "x64.nsh"
!include "Sections.nsh"

!ifndef VERSION
  !define VERSION "0.0.0"
!endif
!ifndef FILEVERSION
  !define FILEVERSION "0.0.0.0"
!endif
!ifndef ARCH
  !define ARCH "x64"
!endif

!define APPNAME "agent-keepalive"
!define EXE "agent-keepalive.exe"
!define DESKTOPEXE "agent-keepalive-desktop.exe"
!define UNINSTKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}"
!define RUNKEY "Software\Microsoft\Windows\CurrentVersion\Run"

; Sign the installer and the uninstaller it writes, when a signing command is given.
!ifdef SIGNCMD
  !finalize '${SIGNCMD} "%1"'
  !uninstfinalize '${SIGNCMD} "%1"'
!endif

Name "${APPNAME}"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\Programs\${APPNAME}"
InstallDirRegKey HKCU "${UNINSTKEY}" "InstallLocation"
BrandingText "${APPNAME} ${VERSION}"

VIProductVersion "${FILEVERSION}"
VIAddVersionKey "ProductName" "${APPNAME}"
VIAddVersionKey "FileDescription" "${APPNAME} setup"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "Copyright (c) 2026 Guru Prasad Srinivasa. MIT License."

!define MUI_ICON "${ICON}"
!define MUI_UNICON "${ICON}"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN
!define MUI_FINISHPAGE_RUN_TEXT "Open ${APPNAME}"
!define MUI_FINISHPAGE_RUN_FUNCTION LaunchApp

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "${LICENSEFILE}"
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Var Update

; Stop a running server and window before their executables are replaced or
; removed. `stop` exits 0 when nothing is running.
!macro StopServer
  ${If} ${FileExists} "$INSTDIR\${EXE}"
    nsExec::Exec '"$INSTDIR\${EXE}" stop'
    Pop $0
    Sleep 500
    ; `stop` waits up to 15 s for a clean exit; whatever is left of this
    ; user's copies (a hung server, a second `open`) is killed so the file
    ; can be replaced.
    ReadEnvStr $1 USERNAME
    nsExec::Exec 'taskkill /F /T /FI "USERNAME eq $1" /IM ${EXE}'
    Pop $0
    nsExec::Exec 'taskkill /F /T /FI "USERNAME eq $1" /IM ${DESKTOPEXE}'
    Pop $0
    Sleep 500
  ${EndIf}
!macroend


Function LaunchApp
  Exec '"$INSTDIR\${EXE}" app'
FunctionEnd

Section "${APPNAME}" SecMain
  SectionIn RO
  !insertmacro StopServer

  SetOutPath "$INSTDIR"
  ; The file can stay locked for a moment after the server exits.
  SetOverwrite try
  ClearErrors
  File "/oname=${EXE}" "${BINARY}"
  ${If} ${Errors}
    Sleep 2000
    SetOverwrite on
    File "/oname=${EXE}" "${BINARY}"
  ${EndIf}
  SetOverwrite on
  !ifdef DESKTOP
    File "/oname=${DESKTOPEXE}" "${DESKTOP}"
  !endif
  File "/oname=agent-keepalive.ico" "${ICON}"
  WriteUninstaller "$INSTDIR\uninstall.exe"

  CreateShortcut "$SMPROGRAMS\${APPNAME}.lnk" "$INSTDIR\${EXE}" "app" "$INSTDIR\agent-keepalive.ico" 0 SW_SHOWNORMAL "" "Open ${APPNAME}"

  WriteRegStr HKCU "${UNINSTKEY}" "DisplayName" "${APPNAME}"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTKEY}" "Publisher" "${APPNAME}"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayIcon" "$INSTDIR\agent-keepalive.ico"
  WriteRegStr HKCU "${UNINSTKEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTKEY}" "URLInfoAbout" "https://github.com/gurupras/agent-keepalive-manager"
  WriteRegStr HKCU "${UNINSTKEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKCU "${UNINSTKEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoRepair" 1
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  IntFmt $0 "0x%08X" $0
  WriteRegDWORD HKCU "${UNINSTKEY}" "EstimatedSize" "$0"
SectionEnd

Section /o "Start at login" SecLogin
  WriteRegStr HKCU "${RUNKEY}" "${APPNAME}" '"$INSTDIR\${EXE}" open --no-browser'
SectionEnd

Section "-finish"
  ${IfNot} ${SectionIsSelected} ${SecLogin}
    DeleteRegValue HKCU "${RUNKEY}" "${APPNAME}"
  ${EndIf}
  ${If} $Update == 1
    Exec '"$INSTDIR\${EXE}" app'
  ${EndIf}
SectionEnd

!insertmacro MUI_FUNCTION_DESCRIPTION_BEGIN
  !insertmacro MUI_DESCRIPTION_TEXT ${SecMain} "The ${APPNAME} program, its window and a Start-menu shortcut."
  !insertmacro MUI_DESCRIPTION_TEXT ${SecLogin} "Start the ${APPNAME} server in the background when you sign in."
!insertmacro MUI_FUNCTION_DESCRIPTION_END

; After the sections, so that ${SecLogin} is defined.
Function .onInit
  !if "${ARCH}" == "arm64"
    ${IfNot} ${IsNativeARM64}
      MessageBox MB_ICONSTOP "This is the Arm64 setup. Download the x64 setup for this computer." /SD IDOK
      Abort
    ${EndIf}
  !endif

  StrCpy $Update 0
  ${GetParameters} $R0
  ClearErrors
  ${GetOptions} $R0 "/UPDATE" $R1
  ${IfNot} ${Errors}
    StrCpy $Update 1
  ${EndIf}

  ; Keep the start-at-login choice of an existing install.
  ReadRegStr $R2 HKCU "${RUNKEY}" "${APPNAME}"
  ${If} $R2 != ""
    !insertmacro SelectSection ${SecLogin}
  ${EndIf}
FunctionEnd

Section "Uninstall"
  !insertmacro StopServer
  Delete "$INSTDIR\${EXE}"
  Delete "$INSTDIR\${DESKTOPEXE}"
  Delete "$INSTDIR\agent-keepalive.ico"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\${APPNAME}.lnk"
  DeleteRegValue HKCU "${RUNKEY}" "${APPNAME}"
  DeleteRegKey HKCU "${UNINSTKEY}"
  ; Sessions, plans and the ledger live in the user's data directory and are
  ; kept, so a reinstall picks up where this one left off.
SectionEnd
