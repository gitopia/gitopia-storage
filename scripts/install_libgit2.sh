#! /bin/sh

git clone https://github.com/libgit2/libgit2.git ~/libgit2
cd ~/libgit2
git checkout v1.3.0
mkdir build && cd build
cmake ..
cmake --build . --target install