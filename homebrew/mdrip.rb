#
# assets/formula.rb
#
# Copyright (c) 2016 Junpei Kawamoto
#
# This software is released under the MIT License.
#
# http://opensource.org/licenses/mit-license.php
#
class Mdrip < Formula
  desc ""
  homepage "https://github.com/dougbeal/mdrip"
  version "{{.Version}}"

  depends_on "go"
  
  if Hardware::CPU.is_64_bit?
    url "https://github.com/dougbeal/mdrip/releases/download/v{{.Version}}/{{.FileName64}}"
    sha256 "{{.Hash64}}"
  else
    url "https://github.com/dougbeal/mdrip/releases/download/v{{.Version}}/{{.FileName386}}"
    sha256 "{{.Hash386}}"
  end

  def install
    bin.install "mdrip"
  end

  test do
    system "mdrip"
  end

end
