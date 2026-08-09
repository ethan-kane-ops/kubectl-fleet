# Regenerate demo.gif: vhs demo.tape (from repo root)
#
# The recorded commands run through real `kubectl` plugin dispatch, but
# demo/kubectl-fleet on PATH is a canned stub, not the real binary — see
# demo/kubectl-fleet. Output mirrors the sample blocks already in README.md.

Output demo.gif

Set Shell "bash"
Set FontSize 18
Set Width 1000
Set Height 560
Set Theme "Dracula"
Set Padding 20
Set TypingSpeed 40ms

Hide
Type "export PATH=$(pwd)/demo:$PATH"
Enter
Type "clear"
Enter
Show

Type "kubectl fleet status --contexts '^prod-'"
Sleep 500ms
Enter
Sleep 2s

Type "kubectl fleet get deploy api -n payments --contexts '^prod-'"
Sleep 500ms
Enter
Sleep 2s

Type "kubectl fleet contexts --check"
Sleep 500ms
Enter
Sleep 2500ms
