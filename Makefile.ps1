$dt = Get-Date -Format "yyy-MM-dd_HHMMss"
$headhash = git rev-parse --short HEAD
$tag = "$($dt)-$($headhash)"
$imageName = "jirm/ww:$($tag)"
$imageLatest = "jirm/ww:latest"

$minisignKey = "W:\keys\jirm-minisign-2020.key"

if ($Args[0] -eq "build") {

    if ($Args[1] -eq "docker") {
        Write-Host "--> Building $($imageName)" -ForegroundColor Green
        docker build --tag $imageName --tag $imageLatest .
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
        Write-Host "Pushing jirm-main"
        git push gjirm jirm-main
    
} elseif ($args[0] -eq "tag") {
    Write-Host "Creating new tag: $tag"
    $version = Read-Host "Enter version (vX.X.X)"
    git tag -a $version -m "$tag"
    git push --tags gjirm jirm-main

} else {
    Write-Host "None!"
}
