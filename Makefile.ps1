$dt = Get-Date -Format "yyy-MM-dd_HHMMss"
$headhash = git rev-parse --short HEAD
$tag = "$($dt)-$($headhash)"
$imageName = "jirm/ww:$($tag)"
$imageLatest = "jirm/ww:latest"

$minisignKey = "W:\keys\jirm-minisign-2020.key"

if ($Args[0] -eq "build") {

    if ($Args[1] -eq "docker") {
        Write-Host "--> Building $($imageName)" -ForegroundColor Green
        docker build -f Dockerfile_original --tag $imageName --tag $imageLatest .
        If ($lastExitCode -eq "0") {
            Write-Host "--> $($imageName) successfully build!" -ForegroundColor Green
        } else {
            Write-Host "--X $($imageName) build failed!" -ForegroundColor Red
        }
    } elseif ($args[1] -eq "cli") {
        Write-Host "--> Building WebWormhole CLI version $tag" -ForegroundColor Green
        go mod download
        go build -o ww.exe .\cmd\ww
        Write-Host "--> Building CLI" -ForegroundColor Green
        minisign -Sm ww.exe -s $minisignKey -c "WebWormhole CLI version $tag - signed $(Split-Path -Leaf $minisignKey)" -t "WebWormhole CLI version $tag - signed $(Split-Path -Leaf $minisignKey)"
    } else {
        Write-Host "None!"
    }
    
} elseif ($args[0] -eq "run") {
    Write-Host "Run"

} elseif ($args[0] -eq "push") {
        Write-Host "--> Pushing jirm-main" -ForegroundColor Green
        git push gjirm jirm-main
    
} elseif ($args[0] -eq "tag") {
    Write-Host "--> Creating new tag: $tag"  -ForegroundColor Green
    $version = Read-Host "Enter version (vX.X.X)"
    git tag -a $version -m "$tag"
    git push --tags gjirm jirm-main

} elseif ($args[0] -eq "release") {
    Write-Host "--> Releasing"  -ForegroundColor Green
    $mPwd = $(Read-Host -AsSecureString "Enter minisign key password" | ConvertFrom-SecureString -AsPlainText)
    Write-Host "--> Testing minisign key password" -ForegroundColor Green
    $mPwd | minisign -Sm .\.gitignore -s $minisignKey
    if ($lastExitCode -ne "0") {
        Write-Host "--X Minisign key password not match!" -ForegroundColor Red
        exit 1
    }
    Write-Host "--> Password is OK" -ForegroundColor Green
    Remove-Item .\.gitignore.minisig
    $mPwd | Set-Content .\pwd -NoNewline
    goreleaser release --clean
    Remove-Item .\pwd

} else {
    Write-Host "None!"
}
